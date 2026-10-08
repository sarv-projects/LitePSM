package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/egress"
	"github.com/sarv-projects/litespm/internal/envref"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/lifecycle"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/state"
)

// install_mcp.go — installing a catalog MCP server into agent host configs.
//
// # Why this is a config write and not a download
//
// Every installable MCP row carries EITHER a launch line (`command` + `args`,
// the real launch line from the upstream registry's package entry) OR a remote
// endpoint (`url` + its transport, from the registry's `remotes[]`) — and
// nothing else needed to run a server: no archive, no digest, no manifest. So
// an MCP install is not a fetch-and-extract; it is a registration. The entry
// goes into the same container the bridge occupies, under the server's own
// name, and the host either spawns it (stdio) or connects to it (remote).
//
// # What the catalog does and does NOT carry, and what that means
//
//   - `url` is published only for rows whose upstream manifest states one, and
//     it is written as a URL-only entry in the host's own documented spelling —
//     never with a command, never with a guessed discriminator, and only to a
//     host whose remote spec verifies (LPSM-HOST-REMOTE-UNSUPPORTED otherwise).
//     The endpoint is refused at plan time, at sealed-plan replay and at
//     execute time by the shared egress guard (LPSM-EGRESS-BLOCKED), so an
//     unsafe URL never reaches an approval prompt or a config file.
//   - `transport` on the older awesome-list rows is derived from an emoji in
//     the upstream registry's README table (a cloud emoji without a house
//     means "sse"): 1,665 of 4,079 rows claim `sse` while carrying a local
//     `command`+`args` and NO URL. For those rows transport is recorded, never
//     obeyed: writing a remote entry without a URL would produce a config every
//     host rejects.
//   - `env` is absent for every row. Real MCP servers frequently need a token,
//     so an installed entry can be registered and still fail at first launch.
//     That is reported explicitly rather than left for the user to discover,
//     and `--env` on a remote entry is refused (LPSM-REMOTE-ENV-REFUSED)
//     before the plan is sealed, not silently dropped.
//
// The result is an honest registration of what the catalog actually knows,
// with its limits stated.

// hostEnvSpec pairs a written host with the substitution rules that were applied
// there, so the CLI can report what each host will actually do with the value.
type hostEnvSpec struct {
	HostID string
	Spec   envref.Spec
}

// mcpInstallBinding is the execution-relevant MCP configuration captured in
// an install plan. The same value is recorded in every HostChange so a single
// approval binds the exact target set and the exact runtime descriptor.
type mcpInstallBinding struct {
	Hosts    []string
	Runtime  *domain.RuntimeDescriptor
	EnvNames []string
	Force    bool
	// ExplicitHosts marks a target set the USER named with --host. An
	// explicitly named host is never filtered away for capability: the user
	// asked for exactly that host, so an incapable one fails by name with
	// LPSM-INSTALL-TARGET-UNAVAILABLE instead of being silently dropped. An
	// unmarked set is LiteSPM's own default choice (every registered bridge
	// host) and is filtered to the hosts that can express the runtime's
	// transport before the plan is sealed, with the drops reported.
	ExplicitHosts bool
}

type mcpInstallPlanValue struct {
	Runtime  domain.RuntimeDescriptor `json:"runtime"`
	EnvNames []string                 `json:"envNames,omitempty"`
	Force    bool                     `json:"force,omitempty"`
}

func mcpInstallPlanValueJSON(binding *mcpInstallBinding) string {
	if binding == nil || binding.Runtime == nil {
		return ""
	}
	value := mcpInstallPlanValue{
		Runtime:  *binding.Runtime,
		EnvNames: append([]string(nil), binding.EnvNames...),
		Force:    binding.Force,
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func uniqueHostIDs(hosts []string) []string {
	seen := make(map[string]bool, len(hosts))
	unique := make([]string, 0, len(hosts))
	for _, hostID := range hosts {
		if hostID == "" || seen[hostID] {
			continue
		}
		seen[hostID] = true
		unique = append(unique, hostID)
	}
	return unique
}

// remoteEndpointPolicy is the egress posture applied to a USER-registered
// remote MCP endpoint at plan time, at sealed-plan replay and at execute time
// (.slim/deepwork/b1-remote-mcp-research.md §2.3/§2.4): https only, plain http
// only for loopback (a local dev endpoint), same-origin redirects bounded to 3
// hops, and deliberately no AllowPrivate — an RFC1918/ULA literal is refused
// here by choice, because a LAN server is both a real use case and the
// canonical SSRF pivot and needs a consent-visible opt-in that does not exist
// yet (Decision 4). No DNS runs at plan time: a config write is not a
// connection, so an offline or behind-VPN install is not refused here — the
// dial-time guard (internal/egress, through mcpclient) is the control that
// cannot be defeated by DNS rebinding.
func remoteEndpointPolicy() egress.Policy {
	return egress.Policy{
		AllowLoopbackHTTP:   true,
		SameOriginRedirects: true,
		MaxRedirects:        3,
	}
}

// checkRemoteEndpoint runs the static half of the egress guard over one
// endpoint: egress.CheckURL (parseable, https or loopback-http, a host, no
// embedded credentials, no IP literal in a range no policy may allow) plus the
// destination rule for a literal IP host — CheckURL deliberately leaves
// RFC1918/loopback literals to the resolve/dial layers, and at plan time there
// is no dial, so the literal is classified here instead. Every refusal is
// domain.ErrEgressBlocked (LPSM-EGRESS-BLOCKED): no plan and no approval
// prompt exists for an unsafe URL. No DNS is performed.
func checkRemoteEndpoint(endpoint string) error {
	p := remoteEndpointPolicy()
	if err := egress.CheckURL(endpoint, p); err != nil {
		return err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return domain.ErrEgressBlocked(endpoint, fmt.Sprintf("unparseable URL: %v", err))
	}
	if addr, perr := netip.ParseAddr(u.Hostname()); perr == nil && !egress.IsAllowedAddr(addr, p) {
		return domain.ErrEgressBlocked(endpoint, fmt.Sprintf(
			"host %q is an address this policy does not permit (a private or otherwise non-public destination must be opted into explicitly)",
			u.Hostname()))
	}
	return nil
}

// remoteEnvRefusal is the typed refusal for --env on a remote (URL) entry. No
// host documents a name→header mapping for a remote entry (codex spells it
// bearer_token_env_var, pi/grok expand ${VAR} inside headers, opencode takes
// {env:VAR}), so accepting a bare variable name would write an entry that looks
// credentialed and is not.
func remoteEnvRefusal(subject, hostID string) error {
	details := map[string]any{"target": subject}
	if hostID != "" {
		details["hostId"] = hostID
	}
	return domain.NewError("LPSM-REMOTE-ENV-REFUSED", fmt.Sprintf(
		"--env cannot be forwarded for the remote (URL) target %q: no host documents how a bare variable name maps to a remote header, "+
			"so writing it would look credentialed and would not be; add the header to the host's config by hand", subject),
		details)
}

// entryTransportLabel names the transport of a WRITTEN entry in the registry
// vocabulary: "stdio" for a launch line, or the remote entry's own token (the
// empty string means streamable-http, which is what the writer registered).
// It is deliberately distinct from the listing's claimed transport, which is
// an emoji-derived record of what the catalog said and nothing more.
func entryTransportLabel(entry host.ServerEntry) string {
	if strings.TrimSpace(entry.Endpoint) == "" {
		return "stdio"
	}
	if t := strings.TrimSpace(entry.Transport); t != "" {
		return t
	}
	return host.TransportStreamableHTTP
}

// mcpEntryTreeDigest is the content-bound tree digest of a registration
// install (S1/S2: no CAS tree exists, so the digest covers the registered
// entry). The endpoint is the FIRST field so a stdio and a remote registration
// of the same listing can never collide: "\x00npx\x00-y\x00demo" and
// "<endpoint>\x00\x00" are distinct inputs by construction.
func mcpEntryTreeDigest(endpoint, command string, args []string) string {
	return domain.ComputeBytesDigest([]byte(endpoint + "\x00" + command + "\x00" + strings.Join(args, "\x00")))
}

// validateSealedRuntime re-runs the plan-time gates over the runtime descriptor
// sealed into an approved plan before it is executed. The plan builder already
// refused these shapes, so anything arriving here is a hand-crafted or mutated
// plan document: it fails closed with a typed error rather than writing what it
// says. subject names what is being installed in the --env refusal (the
// listing id), because a sealed ValueJSON carries no entry name of its own.
func validateSealedRuntime(planID, hostID, subject string, value mcpInstallPlanValue) error {
	command := strings.TrimSpace(value.Runtime.Command)
	endpoint := strings.TrimSpace(value.Runtime.Endpoint)
	switch {
	case command == "" && endpoint == "":
		return domain.ErrPlanStale(planID, fmt.Sprintf("MCP runtime descriptor for host %q is missing or invalid", hostID))
	case command != "" && endpoint != "":
		return domain.ErrPlanStale(planID, fmt.Sprintf(
			"MCP runtime descriptor for host %q names both a command and an endpoint; an entry is one transport", hostID))
	}
	if endpoint == "" {
		return nil // stdio: no endpoint to guard
	}
	if len(value.EnvNames) > 0 {
		return remoteEnvRefusal(subject, hostID)
	}
	return checkRemoteEndpoint(endpoint)
}

// mcpInstallBindingFromPlan validates the approved plan's target details and
// returns only those pinned values. It deliberately does not re-resolve a
// missing host set or runtime descriptor from mutable machine/catalog state.
func mcpInstallBindingFromPlan(ctx context.Context, plan *domain.InstallPlan, listing *domain.Listing, scope domain.InstallScope) (*mcpInstallBinding, error) {
	if plan == nil || listing == nil || listing.Kind != domain.KindMCP {
		return nil, fmt.Errorf("MCP install plan is missing its listing or plan document")
	}
	if len(plan.HostChanges) == 0 {
		return nil, domain.ErrInstallTargetUnavailable("approved plan contains no MCP host targets; prepare a new plan after configuring a host")
	}
	entryName, err := serverEntryNameFor(listing)
	if err != nil {
		return nil, domain.ErrPlanStale(plan.PlanID, "the listing no longer has a usable MCP entry name")
	}
	binding := &mcpInstallBinding{}
	seen := make(map[string]bool, len(plan.HostChanges))
	var expectedValue *mcpInstallPlanValue
	for _, change := range plan.HostChanges {
		if change.HostID == "" || seen[change.HostID] {
			return nil, domain.ErrPlanStale(plan.PlanID, "MCP host target set is empty or contains duplicates")
		}
		seen[change.HostID] = true
		if change.Action != "register_command" || change.EntryKey != entryName || change.ConfigPath == "" {
			return nil, domain.ErrPlanStale(plan.PlanID, fmt.Sprintf("MCP host change for %q is incomplete or does not match the listing", change.HostID))
		}
		adapter, err := host.GetAdapter(change.HostID)
		if err != nil {
			return nil, domain.ErrPlanStale(plan.PlanID, fmt.Sprintf("MCP target host %q is no longer supported", change.HostID))
		}
		currentPath, err := adapter.DetectConfig(ctx, scope)
		if err != nil || filepath.Clean(currentPath) != filepath.Clean(change.ConfigPath) {
			return nil, domain.ErrPlanStale(plan.PlanID, fmt.Sprintf("MCP config target for host %q changed after the plan was prepared", change.HostID))
		}
		var value mcpInstallPlanValue
		if err := json.Unmarshal([]byte(change.ValueJSON), &value); err != nil {
			return nil, domain.ErrPlanStale(plan.PlanID, fmt.Sprintf("MCP runtime descriptor for host %q is missing or invalid", change.HostID))
		}
		// One launch line only (a command or an endpoint, never both), the
		// endpoint's static egress rules (a hand-crafted or replayed plan must
		// not smuggle an unsafe URL past the plan gate) and the remote --env
		// refusal all re-run here: this is the last gate before a write.
		if err := validateSealedRuntime(plan.PlanID, change.HostID, listing.ID, value); err != nil {
			return nil, err
		}
		if expectedValue == nil {
			copyValue := value
			expectedValue = &copyValue
		} else if !reflect.DeepEqual(*expectedValue, value) {
			return nil, domain.ErrPlanStale(plan.PlanID, "MCP host changes disagree about the approved runtime descriptor")
		}
		binding.Hosts = append(binding.Hosts, change.HostID)
	}
	if expectedValue == nil {
		return nil, domain.ErrPlanStale(plan.PlanID, "approved plan contains no MCP runtime descriptor")
	}
	runtime := expectedValue.Runtime
	binding.Runtime = &runtime
	binding.EnvNames = append([]string(nil), expectedValue.EnvNames...)
	binding.Force = expectedValue.Force
	return binding, nil
}

// mcpInstallOutcome is what the CLI prints and the daemon returns.
type mcpInstallOutcome struct {
	InstallID string
	Hosts     []*host.EntryInstallResult
	Entry     host.ServerEntry
	// EnvSpecs is populated only when the user asked for variables, and only for
	// hosts whose documented behaviour is known.
	EnvSpecs []hostEnvSpec
	// ClaimedTransport is the listing's transport field, kept for the record
	// only. See the file comment.
	ClaimedTransport string
	Backups          []string
}

// installMCPFromListing registers a catalog MCP server with the given hosts.
//
// hosts is the exact approved target set. The installer never re-resolves an
// empty set from machine state: callers must capture targets in the plan before
// approval, and an empty set fails closed.
func installMCPFromListing(
	ctx context.Context,
	db *state.DB,
	dataRoot string,
	listing *domain.Listing,
	version string,
	scope domain.InstallScope,
	hosts []string,
	force bool,
	runtime *domain.RuntimeDescriptor,
	envNames []string,
) (*mcpInstallOutcome, error) {
	if listing.Kind != domain.KindMCP {
		return nil, fmt.Errorf("installMCPFromListing: %s is kind %q, not mcp", listing.ID, listing.Kind)
	}

	// Installability gate (Phase 0.1/T3): heuristic awesome-list rows are
	// discovery_only metadata with no proven version or launch line. Refuse
	// with a typed error rather than installing a guessed command.
	if !listing.IsInstallable() {
		return nil, domain.ErrNotInstallable(listing.ID,
			"this listing is discovery-only metadata from an awesome-list with no authoritative manifest; no version or launch line was proven")
	}

	// The launch line comes from the published version record's component
	// runtime, not from the listing: listings carry no command at all. A
	// remote row publishes an endpoint instead of a command, and exactly one
	// of the two must be present — an entry is one transport, and a host that
	// received both would start whichever its own rule prefers, not ours.
	if runtime == nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			"the catalog publishes no launch line (neither a command nor an endpoint) for this listing")
	}
	command := strings.TrimSpace(runtime.Command)
	endpoint := strings.TrimSpace(runtime.Endpoint)
	switch {
	case command == "" && endpoint == "":
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			"the published runtime descriptor names neither a command nor an endpoint, so there is nothing to register")
	case command != "" && endpoint != "":
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			"the published runtime descriptor names both a command and an endpoint; an entry is one transport")
	}
	name, err := serverEntryNameFor(listing)
	if err != nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID, err.Error())
	}
	if err := host.ValidateServerEntryName(name); err != nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID, err.Error())
	}

	if len(hosts) == 0 {
		return nil, domain.ErrInstallTargetUnavailable(
			"no agent host has a verified LiteSPM bridge entry; run 'litespm host setup <host-id>' first, " +
				"or pass --host to name one explicitly")
	}
	if endpoint != "" {
		// --env has no documented remote-header mapping (see the file
		// comment): refuse with the typed code here rather than letting the
		// per-host writer refuse the first config mid-install.
		if len(envNames) > 0 {
			return nil, remoteEnvRefusal(listing.ID, hosts[0])
		}
		// Third and last run of the static egress guard (plan, sealed-plan
		// replay, execute): the config is about to be written, so re-check
		// rather than trust that the plan it came from is the one in flight.
		if err := checkRemoteEndpoint(endpoint); err != nil {
			return nil, err
		}
		// Preflight the capability of every target BEFORE writing any of them
		// (the same rule the collision check below follows): a set the plan
		// gate could not see — a hand-built one — must not half-install.
		transport := host.RegistryRemoteTransport(runtime.Type)
		for _, hostID := range hosts {
			if reason := remoteHostCapabilityReason(hostID, transport); reason != "" {
				return nil, domain.ErrInstallTargetUnavailable(fmt.Sprintf("host %q %s", hostID, reason))
			}
		}
	}
	if err := authorizeMCPInstallPolicy(ctx, db, dataRoot, listing, scope, hosts); err != nil {
		return nil, err
	}

	entry := host.ServerEntry{
		Name:     name,
		EnvNames: append([]string{}, envNames...),
	}
	if endpoint != "" {
		// Remote: URL and transport only — never a command, never args, and
		// never env (refused above). The transport is the registry's own
		// vocabulary, mapped through the host discriminator the writer will
		// emit; an unrecognised token fails closed in the capability gate.
		entry.Endpoint = endpoint
		entry.Transport = host.RegistryRemoteTransport(runtime.Type)
	} else {
		entry.Command = command
		entry.Args = append([]string{}, runtime.Args...)
	}
	backupDir := filepath.Join(dataRoot, "backups")
	outcome := &mcpInstallOutcome{
		Entry:            entry,
		ClaimedTransport: runtime.Type,
	}

	// Validate the names, and confirm every target host can express them, BEFORE
	// any config is touched. Writing the server entry and then failing on the
	// environment would leave the user with a half-installed server that cannot
	// start.
	if err := envref.ValidateNames(entry.EnvNames); err != nil {
		return nil, err
	}
	for _, hostID := range hosts {
		spec, ok := host.EnvForwarding(hostID)
		if !ok {
			if len(entry.EnvNames) > 0 {
				adapter, _ := host.GetAdapter(hostID)
				name := hostID
				if adapter != nil {
					name = adapter.Descriptor().DisplayName
				}
				return nil, fmt.Errorf(
					"host %q (%s) cannot carry a forwarded environment variable — its MCP entry has no environment field, "+
						"or no documented spelling for one — so --env would be silently ignored there; "+
						"install without --env and add the variable to that host's config by hand",
					name, hostID)
			}
			continue
		}
		outcome.EnvSpecs = append(outcome.EnvSpecs, hostEnvSpec{HostID: hostID, Spec: spec})
	}

	// Check every host BEFORE writing any of them, so a collision on the third
	// host cannot leave the first two modified.
	for _, hostID := range hosts {
		adapter, err := host.GetAdapter(hostID)
		if err != nil {
			return nil, err
		}
		if force {
			continue
		}
		names, err := host.ListServerEntries(ctx, hostID, scope)
		if err != nil {
			continue // InstallServerEntry reports the real parse/IO failure
		}
		for _, existing := range names {
			if existing == name {
				return nil, domain.ErrNameConflict(fmt.Sprintf(
					"host %q already registers a server named %q; re-run with --force to replace it",
					adapter.Descriptor().DisplayName, name))
			}
		}
	}

	// State writes need a database; refuse before any config is touched.
	if db == nil {
		return nil, fmt.Errorf("installing %s requires a state database", listing.ID)
	}

	// The install is one unit (ARCH/33 §5): each host config write registers
	// its own compensation as it happens, and every state row lands in one
	// SQLite transaction. A failure at any point restores every config
	// byte-for-byte (LIFO) and leaves no state row behind — the two used to be
	// independent, so a state failure stranded edited configs with nothing
	// recording them.
	txn := lifecycle.BeginInstall(ctx)
	defer txn.RollbackUnlessCommitted()

	for _, hostID := range hosts {
		result, err := host.InstallServerEntry(ctx, hostID, entry, host.EntryInstallOptions{
			BackupDir: backupDir,
			Scope:     scope,
			Force:     force,
		})
		if err != nil {
			return nil, errors.Join(err, txn.Rollback())
		}
		outcome.Hosts = append(outcome.Hosts, result)
		written := result
		txn.Undo(func(ctx context.Context) error { return undoHostConfigWrite(written) })
	}

	outcome.InstallID = fmt.Sprintf("inst_%s_%s_%s", scope, safeInstallIDPart(listing.ID), safeInstallIDPart(version))
	// S1/S2: the install row carries its real kind, a content-bound tree
	// digest (no CAS tree exists for a registration install, so the digest
	// covers the registered entry — endpoint first, so a stdio and a remote
	// registration of the same listing can never share a TreeDigest), and the
	// first config path as install_path.
	entryDigest := mcpEntryTreeDigest(entry.Endpoint, entry.Command, entry.Args)
	installPath := ""
	if len(outcome.Hosts) > 0 {
		installPath = outcome.Hosts[0].ConfigPath
	}
	rec := &domain.InstallRecord{
		InstallID:   outcome.InstallID,
		ListingID:   listing.ID,
		Kind:        domain.KindMCP,
		Version:     version,
		TreeDigest:  entryDigest,
		InstallPath: installPath,
		Scope:       scope,
		Status:      domain.InstallActive,
		InstalledAt: time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	// S3/S4 + S5: install row, component row, one host_registrations row per
	// config modified and one deployment-ledger row per owned write — all in
	// one transaction. The ledger row carries the node-level pre-image (the
	// fingerprint of the entry this write replaced, "" when none existed) and
	// the node-level post-image (the entry as written), so the Reconcile
	// pre-image arm is live and uninstall can distinguish "user edited our
	// node" from "the file changed elsewhere" (ARCH/33 §4).
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := db.SaveInstallExec(ctx, tx, rec); err != nil {
			return fmt.Errorf("record the install: %w", err)
		}
		// S2: component_id is distinct from install_id
		// (<install>#<kind>/<name>); the provider FK resolves against it.
		if err := db.SaveInstallComponentExec(ctx, tx, &domain.InstallComponentRecord{
			InstallID:     outcome.InstallID,
			Kind:          domain.ComponentMCPProvider,
			ComponentName: name,
			Path:          installPath,
		}); err != nil {
			return fmt.Errorf("record the installed component: %w", err)
		}
		for _, result := range outcome.Hosts {
			if err := db.SaveHostRegistrationExec(ctx, tx, &domain.HostRegistrationRecord{
				HostID:           result.HostID,
				Scope:            scope,
				ConfigPath:       result.ConfigPath,
				ConfigFormat:     string(hostConfigFormatFor(result.HostID)),
				ManagedEntryKey:  name,
				EntryFingerprint: result.Fingerprint,
				RegisteredAt:     time.Now().UTC(),
				Status:           "active",
			}); err != nil {
				return fmt.Errorf("record the host registration: %w", err)
			}
			m := lifecycle.OwnedWrite(outcome.InstallID, listing.ID,
				result.HostID, string(scope), result.ConfigPath,
				hostStructureTypeFor(result.HostID), hostLocatorFor(result.HostID, name),
				result.PriorFingerprint, result.Fingerprint, result.PriorEntry, result.BackupPath)
			if err := deployment.SaveMutationExec(ctx, tx, m); err != nil {
				return fmt.Errorf("record the deployment mutation: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, errors.Join(err, txn.Rollback())
	}
	// State is durable: the configs stay. From here the deferred rollback is
	// a no-op.
	if err := txn.Commit(); err != nil {
		return nil, err
	}

	return outcome, nil
}

func authorizeMCPInstallPolicy(ctx context.Context, db *state.DB, dataRoot string, listing *domain.Listing, scope domain.InstallScope, hosts []string) error {
	auth, authorized := installAuthorizationFrom(ctx)
	if !authorized {
		return domain.ErrUnauthorized("package.install", "no recorded plan and approval authorize this install")
	}
	cfg, _ := config.LoadCurrentConfig()
	engine, err := newPolicyEngine(db, dataRoot, policyDefaultsFrom(cfg))
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	effects := []policy.EffectDeclaration{
		{Effect: policy.EffectPackageInstall, Provenance: domain.ProvenanceUserClassified, Target: listing.ID},
		{Effect: policy.EffectHostConfig, Provenance: domain.ProvenanceUserClassified},
	}
	// DELIBERATELY NOT HERE: a targeted `network.outbound` declaration whose
	// Target is the endpoint host (B1 research §2.6, Decision 6). The plan
	// document still declares network.outbound in planEffects — that string is
	// the consent document the approver reads. Feeding it to the policy engine
	// instead would dead-end legitimate installs: invariant 3 denies a
	// restricted target UNCONDITIONALLY, so a loopback dev endpoint
	// (http://127.0.0.1:8080/mcp, the shape Cursor's own docs show) or a host
	// name that does not parse would be denied with no way for the user to
	// consent. Enforcement for remote MCP belongs to the egress guard, at
	// checkRemoteEndpoint (plan/replay/execute) and at dial time. Do not "wire
	// it properly" here.
	for _, hostID := range hosts {
		decision := engine.Evaluate(ctx, policy.PolicyInput{
			Actor:     "user",
			HostID:    hostID,
			Operation: "install",
			TargetRef: listing.ID,
			Effects:   effects,
			Scope:     scope,
		})
		switch decision.Decision {
		case policy.DecisionAllow:
		case policy.DecisionAsk:
			// The command or the bridge already consumed a human approval for
			// this exact plan before reaching the installer.
			if auth.Origin != approvalActorHumanCLI || auth.ApprovalID == "" || auth.PlanID == "" {
				return domain.ErrUnauthorized("package.install", "policy requires a human approval for this host config write")
			}
		default:
			return domain.ErrUnauthorized("package.install", decision.Detail)
		}
	}
	return nil
}

// undoHostConfigWrite restores one host config to exactly the bytes it held
// before InstallServerEntry wrote it. A config this install created is removed
// (pre-install state was "no file"); a config it edited is restored from the
// pre-write backup. InstallTxn runs these in reverse write order, so a
// multi-host failure ends byte-identical to the pre-install machine — and a
// backup that should exist but does not is an error, never a silent skip.
func undoHostConfigWrite(r *host.EntryInstallResult) error {
	if r == nil {
		return nil
	}
	if !r.Created && r.BackupPath == "" {
		return fmt.Errorf("no pre-write backup recorded for %s; refusing to leave the edited config in place", r.ConfigPath)
	}
	if err := host.RollbackConfigWrite(r.ConfigPath, r.BackupPath, r.WrittenDigest); err != nil {
		return fmt.Errorf("rollback host config %s: %w", r.ConfigPath, err)
	}
	return nil
}

// hostConfigFormatFor reports the format of a host's config, for the
// host_registrations record. The descriptor's value is the documented format.
func hostConfigFormatFor(hostID string) string {
	adapter, err := host.GetAdapter(hostID)
	if err != nil {
		return string(host.FormatJSON)
	}
	return adapter.Descriptor().ConfigFormat
}

// serverEntryNameFor picks the key the host will store the server under.
//
// The listing's `name` is the server's own name and is what the user expects to
// see in their agent's server list. Hosts restrict these keys to letters, digits,
// underscore and dash, so a name that violates that is normalized rather than
// refused: refusing would make a legitimate catalog row uninstallable for a reason
// the user cannot act on.
func serverEntryNameFor(listing *domain.Listing) (string, error) {
	candidate := strings.TrimSpace(listing.Name)
	if candidate == "" {
		// Fall back to the last id segment, which is the upstream name.
		if idx := strings.LastIndex(listing.ID, ":"); idx >= 0 && idx+1 < len(listing.ID) {
			candidate = listing.ID[idx+1:]
		}
	}
	if candidate == "" {
		return "", fmt.Errorf("the listing has no usable server name")
	}
	var b strings.Builder
	for _, r := range strings.ToLower(candidate) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	if name == "" {
		return "", fmt.Errorf("the listing name %q contains no usable characters", listing.Name)
	}
	if name == "litespm" || name == "litepsm" {
		return "", fmt.Errorf("the listing name %q collides with the bridge entry", listing.Name)
	}
	return name, nil
}

func printMCPInstall(outcome *mcpInstallOutcome) {
	entry := outcome.Entry
	fmt.Printf("✓ Registered MCP server %s\n", entry.Name)
	fmt.Printf("  Listing ID:  %s\n", outcome.InstallID)
	if entry.Endpoint != "" {
		// A remote registration: the endpoint and its transport ARE the
		// launch line, so print them instead of a command that does not
		// exist.
		fmt.Printf("  Endpoint:    %s (%s)\n", entry.Endpoint, entryTransportLabel(entry))
	} else {
		fmt.Printf("  Command:     %s %s\n", entry.Command, strings.Join(entry.Args, " "))
	}
	for _, h := range outcome.Hosts {
		verb := "added"
		switch {
		case h.Replaced:
			verb = "replaced"
		case h.Created:
			verb = "created"
		}
		fmt.Printf("  • %-12s %s entry in %s\n", h.HostID, verb, h.ConfigPath)
	}
	if entry.Endpoint == "" && outcome.ClaimedTransport != "" && outcome.ClaimedTransport != "stdio" {
		// A row that claims a remote transport but publishes no URL. Say so
		// rather than registering a local entry and implying otherwise.
		fmt.Printf("  Note: the catalog lists this server as %q but publishes no endpoint URL, so the\n"+
			"        local command above is what was registered.\n", outcome.ClaimedTransport)
	}
	printForwardedEnv(outcome)
	if len(outcome.Hosts) > 1 {
		if entry.Endpoint != "" {
			fmt.Printf("  Note: the agent connects to this endpoint on next start; some agents need a restart.\n")
		} else {
			fmt.Printf("  Note: the agent will spawn this server on next start; some agents need a restart.\n")
		}
	}
}

// printForwardedEnv reports what was written for each host and what that host
// will do with it.
//
// The caveats are not decoration. Two of the documented behaviours fail without
// an error — an unset variable becomes an empty string on one host and a silent
// no-op behind an allowlist on another — so a user told only "registered" would
// conclude the credential is in place when it is not.
func printForwardedEnv(outcome *mcpInstallOutcome) {
	names := outcome.Entry.EnvNames
	if strings.TrimSpace(outcome.Entry.Endpoint) != "" {
		// A remote entry never carries --env (it is refused before the plan),
		// so the stdio advice below would send the user at a flag that cannot
		// work. Say what is true instead.
		fmt.Printf("  Note: the catalog publishes no auth headers for this server, and --env is refused for\n" +
			"        remote (URL) entries (LPSM-REMOTE-ENV-REFUSED). Add any credential to the host's\n" +
			"        config by hand.\n")
		return
	}
	if len(names) == 0 {
		fmt.Printf("  Note: the catalog publishes no environment variables for this server. If it\n" +
			"        needs credentials, re-run with --env <VARIABLE_NAME> to forward one.\n")
		return
	}

	// Say plainly that no value was written, so nobody goes looking for a secret
	// in the config and does not find one.
	sample := ""
	if m := outcome.EnvSpecs[0].Spec.EnvMap(names); m != nil {
		for _, n := range envref.SortedNames(names) {
			if sample != "" {
				sample += ", "
			}
			sample += n + " = " + m[n]
		}
	}

	printed := map[string]bool{}
	for _, hs := range outcome.EnvSpecs {
		var how string
		switch hs.Spec.Style {
		case envref.StyleEnvNameList:
			how = fmt.Sprintf("%q in %s (this host takes names only, not values)",
				strings.Join(names, ", "), hs.Spec.NameListField)
		default:
			how = fmt.Sprintf("%s under %q", sample, hs.Spec.Field)
		}
		fmt.Printf("  • %-12s %s\n", hs.HostID, how)
		for _, c := range envref.Caveats(hs.Spec, names) {
			if printed[c] {
				continue
			}
			printed[c] = true
			fmt.Printf("                %s\n", c)
		}
	}
}
