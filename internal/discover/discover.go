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
package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// Discover probes a running server and persists its provider and capability rows.
//
// A probe failure is returned, not swallowed: the caller decides whether an
// unprobeable server should fail the surrounding operation. The rows themselves
// are written only after a complete, successful listing, so a server that dies
// mid-probe cannot leave a half-discovered capability set behind.
func Discover(ctx context.Context, db *state.DB, spec ProviderSpec, opts ...Option) (*DiscoveredProvider, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("provider %q has no command to run", spec.ComponentName)
	}

	session, cleanup, err := dial(ctx, spec, newOptions(opts).supervisor)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	tools, err := mcpclient.ProbeProvider(ctx, session)
	if err != nil {
		return nil, err
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
		Transport:     transportOrDefault(spec.Transport),
		Command:       spec.Command,
		ArgsJSON:      string(argsJSON),
		EnvJSON:       envJSON,
		Status:        "active",
		CreatedAt:     now,
		UpdatedAt:     now,
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
	if spec.Command == "" {
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
		return nil, err
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

// dial starts the server through the provider supervisor (process-group /
// job-object isolation, minimal environment) and completes an MCP session over
// its stdio pipes. The returned cleanup closes the session and terminates the
// process tree.
func dial(ctx context.Context, spec ProviderSpec, sup *provider.Supervisor) (mcpclient.ClientSession, func(), error) {
	if !isStdioTransport(spec.Transport) {
		// The catalog publishes no remote endpoint for any row, so this is
		// unreachable today; it fails loudly rather than pretending to dial.
		return nil, nil, fmt.Errorf("provider %q uses transport %q, which is not supported yet",
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

func transportOrDefault(t string) string {
	if strings.TrimSpace(t) == "" {
		return "stdio"
	}
	return t
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
