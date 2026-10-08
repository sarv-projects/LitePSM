// Package discover turns an installed MCP server into capability rows, and
// invokes a discovered capability.
//
// # Why this exists
//
// Installing a server only writes a config entry. The catalog then knows nothing
// about what that server can DO, so an agent has no way to discover its tools and
// no way to call them. The pieces for closing that gap were all present and
// unwired: `mcpclient` could speak to a server but had no production importer,
// `ProviderRecord`/`CapabilityRecord` and their state writers existed with no
// caller, and `provider.invoke`, `capabilities.search` and
// `capabilities.describe` answered -32601 with the reason "no capability rows are
// persisted to resolve a capabilityId against".
//
// # What it deliberately does not do
//
// It does not persist invocations. An invocation whose process died with the
// daemon is worse than a forgotten one, so the asynchronous invocation registry
// stays where ARCH/34 puts it (DESIGNED) and `invocation.get` /
// `invocation.cancel` keep failing closed with that reason. `provider.invoke`
// itself is synchronous, which is what ARCH/06 §4 and the Bridge tool contract
// (`invoke_capability` -> `{output, isError}`) already specify.
//
// # Remote (URL) providers
//
// A provider is either a local command or a remote endpoint — an entry is one
// transport. A remote spec dials its endpoint through mcpclient's own
// connectors, which wrap every HTTP client in internal/egress (scheme,
// destination, redirect and checked-dial rules run on every request); this
// package deliberately builds no http.Client of its own, because two policies
// would drift and the guard is the only thing standing between a stored URL
// and the link-local metadata service.
//
// The rows rule does not change for a remote provider: capability rows are
// written only after a complete, successful listing. An endpoint that refuses
// authentication or cannot be reached returns a typed error
// (LPSM-PROVIDER-REMOTE-AUTH / LPSM-PROVIDER-REMOTE-CONNECT, or the guard's
// LPSM-EGRESS-BLOCKED) and writes NOTHING — no provider row, no capability
// rows — so an entry that was never connected to has no invented health state
// to render (Decision 8: an unconnected remote entry reads as unknown, never
// as a fabricated ready light).
package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/envref"
	"github.com/sarv-projects/litespm/internal/mcpclient"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/provider"
	"github.com/sarv-projects/litespm/internal/state"
)

// Policy is the slice of internal/policy.Engine that Invoke needs. *policy.Engine
// satisfies it; the interface exists so tests (and other policy sources) can
// stand in without a database-backed engine.
type Policy interface {
	Evaluate(ctx context.Context, input policy.PolicyInput) policy.PolicyDecision
}

// Option configures Discover and Invoke.
type Option func(*options)

type options struct {
	policy     Policy
	supervisor *provider.Supervisor
	actor      string
	hostID     string
	effects    []policy.EffectDeclaration
}

// WithPolicy routes Invoke through a policy engine. Without it Invoke is
// fail-closed: only an invocation explicitly classified read-only (WithEffects)
// proceeds, everything else is denied.
func WithPolicy(p Policy) Option { return func(o *options) { o.policy = p } }

// WithSupervisor makes Discover and Invoke spawn their server through a shared
// provider.Supervisor. The default is a private supervisor scoped to the call.
func WithSupervisor(s *provider.Supervisor) Option { return func(o *options) { o.supervisor = s } }

// WithActor sets the policy actor ("user" | "agent" | "system"); default "user".
func WithActor(actor string) Option { return func(o *options) { o.actor = actor } }

// WithHostID records the calling host in the policy input.
func WithHostID(hostID string) Option { return func(o *options) { o.hostID = hostID } }

// WithEffects replaces the default effect classification of the invocation.
// The default is conservative (a local process is spawned with the user's
// authority). Callers that hold a curated or user classification pass it here;
// an effect set made only of filesystem.read / external.read counts as
// read-only for the no-policy fail-closed rule.
func WithEffects(effects ...policy.EffectDeclaration) Option {
	return func(o *options) { o.effects = append([]policy.EffectDeclaration(nil), effects...) }
}

func newOptions(opts []Option) *options {
	o := &options{actor: "user"}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	if o.supervisor == nil {
		o.supervisor = provider.NewSupervisor()
	}
	return o
}

// defaultInvokeEffects classifies an invocation of an arbitrary stdio MCP
// tool: LiteSPM cannot know what the tool does, but it does know it starts a
// provider process and spawns code under the user's account.
func defaultInvokeEffects() []policy.EffectDeclaration {
	return []policy.EffectDeclaration{
		{Effect: policy.EffectProviderStart, Provenance: domain.ProvenanceRuntimeObserved},
		{Effect: policy.EffectProcessSpawn, Provenance: domain.ProvenanceRuntimeObserved},
	}
}

func isReadOnlyEffects(effects []policy.EffectDeclaration) bool {
	if len(effects) == 0 {
		return false // unclassified is never read-only
	}
	for _, e := range effects {
		if e.Effect != policy.EffectFilesystemRead && e.Effect != policy.EffectExternalRead {
			return false
		}
	}
	return true
}

// authorize is the single policy gate for an invocation. It is evaluated twice:
// before the server is spawned (recorded fingerprint) and after the re-probe
// (current fingerprint), so a capability grant bound to a fingerprint is
// validated against both what was discovered and what the server now serves.
func (o *options) authorize(ctx context.Context, capabilityID, fingerprint string, effects []policy.EffectDeclaration) error {
	if o.policy == nil {
		if isReadOnlyEffects(effects) {
			return nil
		}
		return domain.NewError(domain.CodePolicyDenied,
			fmt.Sprintf("invocation of %s denied: no policy engine configured and the invocation is not classified read-only", capabilityID),
			map[string]any{"capabilityId": capabilityID})
	}
	decision := o.policy.Evaluate(ctx, policy.PolicyInput{
		Actor:             o.actor,
		HostID:            o.hostID,
		Operation:         "invoke",
		TargetRef:         capabilityID,
		CapabilityID:      capabilityID,
		Effects:           effects,
		SchemaFingerprint: fingerprint,
		Scope:             domain.ScopeUser,
	})
	if decision.Decision == policy.DecisionAllow {
		return nil
	}
	code := domain.CodePolicyDenied
	if len(decision.ReasonCodes) > 0 && strings.HasPrefix(decision.ReasonCodes[0], "LPSM-") {
		code = decision.ReasonCodes[0]
	}
	msg := fmt.Sprintf("invocation of %s refused by policy (%s)", capabilityID, decision.Decision)
	if decision.Decision == policy.DecisionAsk {
		msg = fmt.Sprintf("invocation of %s requires an active capability grant (approval required)", capabilityID)
	}
	if decision.Detail != "" {
		msg += ": " + decision.Detail
	}
	return domain.NewError(code, msg, map[string]any{
		"capabilityId": capabilityID,
		"decision":     string(decision.Decision),
		"reasonCodes":  decision.ReasonCodes,
	})
}

// ProviderSpec is one installed MCP server to probe or call.
type ProviderSpec struct {
	// InstallID ties the provider and its capabilities to an install record, so
	// a capability id can always be traced back to what the user installed.
	InstallID string
	// ComponentName is the name the server is registered under.
	ComponentName string
	Command       string
	Args          []string
	// Env is what the host config stores, which for a forwarded variable is a
	// reference such as `${GITHUB_TOKEN}`, not a value. dial resolves it against
	// this process's environment, because the host's expansion does not apply
	// to a server LiteSPM is spawning itself.
	Env map[string]string
	// HostID selects the host's own substitution rules when resolving, because a
	// `${VAR:-fallback}` is expanded by some hosts and left alone by others.
	HostID    string
	Transport string
	// Endpoint is the remote (URL) MCP server to dial instead of launching a
	// process. An entry is one transport, so a spec carries EITHER Command
	// (stdio) OR Endpoint (remote), never both — the same rule the host config
	// writer enforces (internal/host InstallServerEntry). Empty for every stdio
	// provider; non-empty makes the spec remote regardless of which side
	// filled it.
	Endpoint string
}

// Remote failure codes. domain has no remote-connect/auth code today and
// internal/domain belongs to another lane, so the codes are minted here
// through domain.NewError (which derives the category from the code). Both
// satisfy schemas/errors.schema.json's ^LPSM-[A-Z]+-[A-Z0-9_-]+$ pattern.
const (
	// CodeRemoteAuth is a remote endpoint that answered HTTP 401/403: the URL
	// is right and the credentials are missing or wrong. Reporting it as a
	// connection failure would send the operator debugging a network that is
	// fine — the most common remote MCP outcome is a protected endpoint, not
	// a broken one.
	CodeRemoteAuth = "LPSM-PROVIDER-REMOTE-AUTH"
	// CodeRemoteConnect is a remote endpoint that could not be reached or did
	// not complete the MCP exchange: dial/TLS/timeout failure, a non-auth
	// HTTP status, or a protocol error.
	CodeRemoteConnect = "LPSM-PROVIDER-REMOTE-CONNECT"
)

// httpStatusPattern matches mcpclient's non-2xx wrapping ("http error %d: …").
// The status is the only structured fact that error carries, so the
// auth/connection split reads it out of the chain.
var httpStatusPattern = regexp.MustCompile(`http error (\d{3})`)

// httpStatusOf walks err's wrap chain for mcpclient's HTTP status.
func httpStatusOf(err error) (int, bool) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if m := httpStatusPattern.FindStringSubmatch(e.Error()); m != nil {
			if n, convErr := strconv.Atoi(m[1]); convErr == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// isRemoteSpec reports whether the spec dials an endpoint instead of spawning
// a process. The endpoint is the authority: it is the fact the host config
// read back, and a stdio spec never carries one.
func isRemoteSpec(spec ProviderSpec) bool {
	return strings.TrimSpace(spec.Endpoint) != ""
}

// DiscoveredProvider is what a probe found.
type DiscoveredProvider struct {
	ProviderID   string
	InstallID    string
	Capabilities []domain.CapabilityRecord
}

// ProviderIDFor derives a stable provider id from the install and component, so
// re-probing updates rows instead of accumulating duplicates.
func ProviderIDFor(installID, componentName string) string {
	return fmt.Sprintf("prov_%s_%s", sanitizeIDPart(installID), sanitizeIDPart(componentName))
}

func sanitizeIDPart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// validateTarget is the "an entry is one transport" gate every session-opening
// entry point runs (Discover, Invoke, and dial as the last line of defence
// before a spawn or a connection). label is the already-formatted provider the
// error must name: the quoted component name for a spec, the provider id for a
// row read back from state.
//
// A spec naming NEITHER a command nor an endpoint is corrupt state: there is
// nothing to dial or spawn, so the honest answer is a refusal, not a silent
// no-op. A spec naming BOTH is ambiguous — whichever one gets checked first
// would hide the other — so it is refused at the same gate the host config
// writer makes (internal/host InstallServerEntry).
func validateTarget(spec ProviderSpec, label string) error {
	hasCommand := strings.TrimSpace(spec.Command) != ""
	hasEndpoint := isRemoteSpec(spec)
	switch {
	case hasCommand && hasEndpoint:
		return ambiguousTargetError(label)
	case hasEndpoint:
		return nil
	case hasCommand:
		// A stdio command with a remote transport is refused later, at dial,
		// where the "nothing recorded to dial" refusal has always lived.
		return nil
	default:
		return fmt.Errorf("provider %s has no command to run and no endpoint (URL) to connect to", label)
	}
}

// ambiguousTargetError is the shared "an entry is one transport" refusal for a
// spec/row that names both a command and an endpoint.
func ambiguousTargetError(label string) error {
	return fmt.Errorf("provider %s records both a command and an endpoint; an entry is one transport", label)
}

// Discover probes a running server and persists its provider and capability rows.
//
// A probe failure is returned, not swallowed: the caller decides whether an
// unprobeable server should fail the surrounding operation. The rows themselves
// are written only after a complete, successful listing, so a server that dies
// mid-probe cannot leave a half-discovered capability set behind.
func Discover(ctx context.Context, db *state.DB, spec ProviderSpec, opts ...Option) (*DiscoveredProvider, error) {
	// One transport, and it must name a target: a stdio spec needs a command,
	// a remote spec an endpoint. Neither case is corrupt state, and dialling
	// (or spawning) nothing would report success for a server that never ran.
	if err := validateTarget(spec, fmt.Sprintf("%q", spec.ComponentName)); err != nil {
		return nil, err
	}

	session, cleanup, err := dial(ctx, spec, newOptions(opts).supervisor)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	tools, err := mcpclient.ProbeProvider(ctx, session)
	if err != nil {
		// Rows are written only below, after a complete listing: a failed
		// remote probe classifies to a typed error and leaves ZERO rows.
		return nil, classifyRemoteFailure(spec, err)
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("provider %q exposed no tools", spec.ComponentName)
	}

	providerID := ProviderIDFor(spec.InstallID, spec.ComponentName)
	now := time.Now().UTC()

	argsJSON, _ := json.Marshal(spec.Args)
	envJSON := ""
	if len(spec.Env) > 0 {
		if b, err := json.Marshal(spec.Env); err == nil {
			envJSON = string(b)
		}
	}
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID:    providerID,
		InstallID:     spec.InstallID,
		ComponentName: spec.ComponentName,
		Transport:     storedTransport(spec),
		// The endpoint rides into launch_spec_json so a later Invoke can
		// rebuild this exact remote spec without re-reading the host config.
		Endpoint:  spec.Endpoint,
		Command:   spec.Command,
		ArgsJSON:  string(argsJSON),
		EnvJSON:   envJSON,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("record provider: %w", err)
	}

	out := &DiscoveredProvider{ProviderID: providerID, InstallID: spec.InstallID}
	for _, tool := range tools {
		record := domain.CapabilityRecord{
			CapabilityID:      string(domain.NewCapabilityID(domain.InstallID(spec.InstallID), spec.ComponentName, tool.NativeName)),
			ProviderID:        providerID,
			Name:              tool.NativeName,
			Description:       tool.Description,
			InputSchemaJSON:   string(tool.InputSchema),
			SchemaFingerprint: tool.SchemaFingerprint,
			DiscoveredAt:      now,
			UpdatedAt:         now,
		}
		if err := db.SaveCapability(ctx, &record); err != nil {
			return nil, fmt.Errorf("record capability %q: %w", tool.NativeName, err)
		}
		out.Capabilities = append(out.Capabilities, record)
	}
	return out, nil
}

// InvokeResult is a tool call's outcome, shaped like the Bridge contract.
type InvokeResult struct {
	Output  string `json:"output"`
	IsError bool   `json:"isError"`
}

// Invoke calls one discovered capability.
//
// It resolves the capability id, re-reads the provider row for the command, and
// calls the tool. The schema fingerprint is re-checked first: if the server's
// tool schema changed since discovery, the call is refused rather than made
// against arguments nobody validated. That is the drift check
// `mcpclient.DetectDrift` exists for, applied as a gate instead of a report.
//
// Authorization: the call is gated by the policy engine (WithPolicy) before
// the server is spawned and again, against the freshly probed fingerprint,
// before the tool is called. Without a policy the call is fail-closed: it is
// denied unless explicitly classified read-only via WithEffects.
func Invoke(ctx context.Context, db *state.DB, capabilityID string, arguments json.RawMessage, opts ...Option) (*InvokeResult, error) {
	o := newOptions(opts)
	effects := o.effects
	if effects == nil {
		effects = defaultInvokeEffects()
	}
	_, installID, componentName, toolName, err := domain.ParseCapabilityID(capabilityID)
	if err != nil {
		return nil, err
	}
	providerID := ProviderIDFor(string(installID), componentName)

	provider, err := db.GetProvider(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("no provider is registered for capability %q (provider %s): %w", capabilityID, providerID, err)
	}
	capability, err := db.GetCapability(ctx, capabilityID)
	if err != nil {
		return nil, fmt.Errorf("no discovered capability %q: %w", capabilityID, err)
	}

	// The capability id is the authority on which component this is: the
	// providers table stores a component_id, not the display name.
	component := componentName
	if component == "" {
		component = provider.ComponentName
	}
	spec := ProviderSpec{
		InstallID:     provider.InstallID,
		ComponentName: component,
		Command:       provider.Command,
		Endpoint:      provider.Endpoint,
		Transport:     provider.Transport,
	}
	if provider.ArgsJSON != "" {
		if err := json.Unmarshal([]byte(provider.ArgsJSON), &spec.Args); err != nil {
			return nil, fmt.Errorf("provider %s has an unreadable argument list: %w", providerID, err)
		}
	}
	if provider.EnvJSON != "" {
		if err := json.Unmarshal([]byte(provider.EnvJSON), &spec.Env); err != nil {
			return nil, fmt.Errorf("provider %s has an unreadable environment: %w", providerID, err)
		}
	}
	// One transport, one target: a row naming both is corrupt/ambiguous
	// state, and a row naming neither gives dial nothing to open. A remote row
	// is named as such (endpoint, not command) so the refusal tells the
	// operator which fact is missing.
	switch {
	case strings.TrimSpace(spec.Command) != "" && isRemoteSpec(spec):
		return nil, ambiguousTargetError(providerID)
	case strings.TrimSpace(spec.Command) == "" && !isRemoteSpec(spec):
		if isRemoteTransport(spec.Transport) {
			return nil, fmt.Errorf("provider %s has no endpoint recorded; re-run discovery", providerID)
		}
		return nil, fmt.Errorf("provider %s has no command recorded; re-run discovery", providerID)
	}

	// Gate 1 (before any process exists): grant + recorded fingerprint.
	if err := o.authorize(ctx, capabilityID, capability.SchemaFingerprint, effects); err != nil {
		return nil, err
	}

	session, cleanup, err := dial(ctx, spec, o.supervisor)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Re-probe and compare the schema before calling: an argument shape that
	// changed under us is a hard stop, not a warning.
	current, err := mcpclient.ProbeProvider(ctx, session)
	if err != nil {
		return nil, classifyRemoteFailure(spec, err)
	}
	for _, tool := range current {
		if tool.NativeName != toolName {
			continue
		}
		if tool.SchemaFingerprint != capability.SchemaFingerprint {
			return nil, fmt.Errorf("tool %q changed its input schema since discovery (recorded %s, now %s); "+
				"re-run discovery before invoking it", toolName, capability.SchemaFingerprint, tool.SchemaFingerprint)
		}
		// Gate 2: the grant must also hold for the fingerprint the server
		// serves right now (a drifted schema invalidates a prior grant).
		if err := o.authorize(ctx, capabilityID, tool.SchemaFingerprint, effects); err != nil {
			return nil, err
		}
		if len(arguments) == 0 {
			arguments = json.RawMessage("{}")
		}
		result, err := session.CallTool(ctx, toolName, arguments)
		if err != nil {
			if isRemoteSpec(spec) {
				// A transport failure against an endpoint names the endpoint
				// and the auth/connection cause instead of a bare text error.
				return nil, classifyRemoteFailure(spec, err)
			}
			return nil, fmt.Errorf("tool %q failed: %w", toolName, err)
		}
		return &InvokeResult{Output: flattenContent(result.Content), IsError: result.IsError}, nil
	}
	return nil, fmt.Errorf("provider %s no longer exposes a tool named %q", providerID, toolName)
}

// flattenContent renders a tool result's content parts as the single string the
// Bridge contract expects. Non-text parts are named rather than dropped, so a
// caller is never told a call produced nothing when it produced an image.
func flattenContent(parts []mcpclient.ToolContent) string {
	var b strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "", "text":
			b.WriteString(part.Text)
		case "image":
			fmt.Fprintf(&b, "[image: %d base64 chars]", len(part.Data))
		case "resource":
			fmt.Fprintf(&b, "[resource: %s]", part.Text)
		default:
			fmt.Fprintf(&b, "[%s content omitted]", part.Type)
		}
	}
	return b.String()
}

var dialSeq atomic.Uint64

// resolveEnv turns the stored environment (references, names, or values) into
// the concrete variables the server is given. It resolves only variables the
// stored spec names; nothing else from this process's environment is forwarded.
func resolveEnv(spec ProviderSpec) map[string]string {
	if len(spec.Env) == 0 {
		return nil
	}
	// A name-list host (Codex) stores only the name, so its read-back value
	// is empty; a reference-host stores the reference text. Both resolve here,
	// from this process's environment, because the host's own expansion never
	// runs for a server LiteSPM spawns itself.
	hostSpec, _ := envref.SpecFor(spec.HostID)
	lookup := func(k string) (string, bool) { return os.LookupEnv(k) }
	out := make(map[string]string, len(spec.Env))
	for k, v := range spec.Env {
		// Only pass a value we actually have. An unset reference resolves to
		// its own text and a name-list host stores no value; passing either
		// through would hand the server a literal `${VAR}` or an empty string
		// in place of a variable that does not exist.
		resolved, expanded := hostSpec.Resolve(v, lookup)
		if !expanded && resolved == "" {
			continue
		}
		out[k] = resolved
	}
	return out
}

// dial opens an MCP session with the provider.
//
// A spec with an endpoint dials it remotely through mcpclient's guarded
// connectors (internal/egress runs on every request; this package never builds
// an http.Client itself). A spec without one starts the server through the
// provider supervisor (process-group / job-object isolation, minimal
// environment) and completes an MCP session over its stdio pipes. The returned
// cleanup closes the session and, for a stdio dial, terminates the process
// tree; for a remote dial it closes the HTTP session (releasing a legacy
// server-side session with DELETE when one was assigned).
func dial(ctx context.Context, spec ProviderSpec, sup *provider.Supervisor) (mcpclient.ClientSession, func(), error) {
	if isRemoteSpec(spec) {
		if strings.TrimSpace(spec.Command) != "" {
			// Defence in depth: Discover and Invoke validate first, but a
			// spawn-and-dial split would let the host pick which transport
			// it honours, so refuse at the boundary that actually connects.
			return nil, nil, ambiguousTargetError(fmt.Sprintf("%q", spec.ComponentName))
		}
		return dialRemote(ctx, spec)
	}
	if !isStdioTransport(spec.Transport) {
		// A remote transport with no endpoint records nothing to dial. The
		// catalog/host read-back path always carries both together, so this
		// is a hand-edited or half-written record: it fails loudly rather
		// than pretending to dial (or falling back to a spawn).
		return nil, nil, fmt.Errorf("provider %q uses transport %q, which is not supported yet without a recorded endpoint",
			spec.ComponentName, spec.Transport)
	}
	if sup == nil {
		sup = provider.NewSupervisor()
	}
	// A unique id per spawn: StartProvider reuses a still-running provider with
	// the same id, which must never happen for a one-shot call.
	handleID := fmt.Sprintf("%s#%d", ProviderIDFor(spec.InstallID, spec.ComponentName), dialSeq.Add(1))
	handle, err := sup.StartProvider(ctx, handleID, provider.LaunchSpec{
		Executable: spec.Command,
		Args:       spec.Args,
		Env:        resolveEnv(spec),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", spec.Command, err)
	}
	terminate := func() { _ = handle.Terminate(2 * time.Second) }

	session, err := mcpclient.ConnectStdio(ctx, handle.Stdout, handle.Stdin)
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("connect to %s: %w", spec.Command, err)
	}
	cleanup := func() {
		_ = session.CloseSession()
		terminate()
	}
	return session, cleanup, nil
}

// remoteConnector is the mcpclient connector a remote transport selects.
type remoteConnector int

const (
	connectorStreamableHTTP remoteConnector = iota
	connectorLegacy
)

// remoteConnectorFor maps a transport onto the connector that speaks it. The
// vocabulary is every spelling the remote path can actually receive: the
// registry tokens a host config reads back through
// host.RegistryRemoteTransport ("streamable-http", "sse"), the providers.mode
// values a stored row reads back ("remote-http", "legacy-sse"), and the
// per-host discriminator a config may carry literally ("http",
// "streamableHttp", "remote"). A token outside that closed set is a refusal —
// never a guess about what the endpoint speaks.
func remoteConnectorFor(transport string) (remoteConnector, error) {
	switch v := strings.ToLower(strings.TrimSpace(transport)); v {
	case "", "streamable-http", "remote-http", "http", "https", "streamablehttp", "remote":
		return connectorStreamableHTTP, nil
	case "sse", "legacy-sse":
		return connectorLegacy, nil
	default:
		return 0, fmt.Errorf("transport %q is not supported yet for a remote (URL) provider", transport)
	}
}

// isRemoteTransport reports whether t names a remote transport. The empty
// string is not remote — it is the stdio default — so it shares
// remoteConnectorFor's closed set but not its default.
func isRemoteTransport(t string) bool {
	if strings.TrimSpace(t) == "" {
		return false
	}
	_, err := remoteConnectorFor(t)
	return err == nil
}

// dialRemote connects to a remote (URL) endpoint.
//
// The HTTP client is mcpclient's own: ConnectStreamableHTTP/ConnectLegacy wrap
// a nil client through internal/egress's remote policy (https-only with a
// loopback-http exception, checked-IP dial, at most 3 same-origin redirect
// hops, private addresses off). Building a raw http.Client here would put a
// second, weaker policy between a stored URL and the network.
//
// The modern connector performs no I/O at construction (the 2026-07-28
// profile is stateless: no initialize, no session id), so a streamable-http
// refusal surfaces from the first probe; the legacy connector handshakes
// eagerly, so its refusal surfaces here. Both are classified the same way.
func dialRemote(ctx context.Context, spec ProviderSpec) (mcpclient.ClientSession, func(), error) {
	endpoint := strings.TrimSpace(spec.Endpoint)
	connector, err := remoteConnectorFor(spec.Transport)
	if err != nil {
		return nil, nil, fmt.Errorf("provider %q: %w", spec.ComponentName, err)
	}
	switch connector {
	case connectorLegacy:
		client, lerr := mcpclient.ConnectLegacy(ctx, endpoint, nil, nil)
		if lerr != nil {
			return nil, nil, classifyRemoteFailure(spec, lerr)
		}
		return client, func() { _ = client.CloseSession() }, nil
	default:
		client, cerr := mcpclient.ConnectStreamableHTTP(ctx, endpoint, nil, nil)
		if cerr != nil {
			return nil, nil, classifyRemoteFailure(spec, cerr)
		}
		return client, func() { _ = client.CloseSession() }, nil
	}
}

// classifyRemoteFailure turns a transport-level failure against a remote
// endpoint into a typed error that names the endpoint, the component and the
// cause:
//
//   - LPSM-EGRESS-BLOCKED passes through with the endpoint recorded: the SSRF
//     guard refused BEFORE any connection was made, and a caller must be able
//     to tell a policy refusal from an unreachable server. The guard's own
//     reason ("host … is in an address range that is never permitted") is
//     preserved, which is also the evidence that nothing dialled.
//   - HTTP 401/403 becomes LPSM-PROVIDER-REMOTE-AUTH.
//   - anything else becomes LPSM-PROVIDER-REMOTE-CONNECT (dial/TLS/timeout,
//     non-auth status, protocol failure).
//
// A spec with no endpoint returns err untouched: a stdio spawn failure is not
// a remote failure and keeps its local wording.
func classifyRemoteFailure(spec ProviderSpec, err error) error {
	if err == nil || !isRemoteSpec(spec) {
		return err
	}
	endpoint := strings.TrimSpace(spec.Endpoint)

	var guard *domain.LPSMError
	if errors.As(err, &guard) && guard != nil && guard.Code == egressBlockedCode {
		return guard.
			WithDetail("endpoint", endpoint).
			WithDetail("component", spec.ComponentName)
	}

	details := map[string]any{
		"endpoint":  endpoint,
		"component": spec.ComponentName,
		"transport": strings.TrimSpace(spec.Transport),
		"cause":     err.Error(),
	}
	if status, ok := httpStatusOf(err); ok && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		e := domain.NewError(CodeRemoteAuth,
			fmt.Sprintf("remote endpoint %s refused authentication (HTTP %d)", endpoint, status), details)
		e.Details["status"] = status
		e.Cause = err
		return e
	}
	e := domain.NewError(CodeRemoteConnect,
		fmt.Sprintf("remote endpoint %s could not complete the MCP exchange", endpoint), details)
	e.Cause = err
	return e
}

// egressBlockedCode is domain.ErrEgressBlocked's machine code; named here so
// the classification reads as a reference, not a literal to retype.
const egressBlockedCode = "LPSM-EGRESS-BLOCKED"

func transportOrDefault(t string) string {
	if strings.TrimSpace(t) == "" {
		return "stdio"
	}
	return t
}

// storedTransport is the transport the providers row records for a spec.
//
// For a remote spec it is always LiteSPM's own registry vocabulary, never the
// host's discriminator spelling and never the stdio default: state.SaveProvider
// maps that vocabulary onto the providers.mode CHECK, so storing a host token
// like "streamableHttp" verbatim would fail the mode mapping, while
// transportOrDefault's "" → "stdio" would write a row whose mode says local
// while its endpoint says remote — a row Invoke would then refuse as a stdio
// provider with no command. For a stdio spec the recorded transport is
// unchanged.
func storedTransport(spec ProviderSpec) string {
	if isRemoteSpec(spec) {
		if connector, err := remoteConnectorFor(spec.Transport); err == nil && connector == connectorLegacy {
			return "sse"
		}
		return "streamable-http"
	}
	return transportOrDefault(spec.Transport)
}

// isStdioTransport accepts both vocabularies in play: the transport names the
// catalog and domain model speak ("stdio"), and the CHECK-constrained enum the
// providers table stores ("local-stdio"). Treating them as different transports
// would make every discovered provider look remote.
func isStdioTransport(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "stdio", "local-stdio":
		return true
	default:
		return false
	}
}
