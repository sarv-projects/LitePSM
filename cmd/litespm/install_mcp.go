package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/envref"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/state"
)

// install_mcp.go — installing a catalog MCP server into agent host configs.
//
// # Why this is a config write and not a download
//
// Every MCP row in the published catalog carries `command` and `args` — the real
// launch line from the upstream registry's package entry — and nothing else
// needed to start a local server: no archive, no digest, no manifest. So an MCP
// install is not a fetch-and-extract; it is a registration. The entry goes into
// the same container the bridge occupies, under the server's own name, and the
// host spawns it.
//
// # What the catalog does NOT carry, and what that means
//
// Two fields that look authoritative are not, and the difference is load-bearing:
//
//   - `transport` is derived from an emoji in the upstream registry's README
//     table (a cloud emoji without a house means "sse"). 1,665 of 4,079 rows
//     claim `sse` while carrying a local `command`+`args`, and the dataset has no
//     URL field at all. It is therefore recorded, never obeyed: there is nothing
//     to obey it with, and writing a remote entry without a URL would produce a
//     config every host rejects.
//   - `env` is absent for every row. Real MCP servers frequently need a token, so
//     an installed entry can be registered and still fail at first launch. That is
//     reported explicitly rather than left for the user to discover.
//
// The result is an honest stdio registration of what the catalog actually knows,
// with its limits stated.

// hostEnvSpec pairs a written host with the substitution rules that were applied
// there, so the CLI can report what each host will actually do with the value.
type hostEnvSpec struct {
	HostID string
	Spec   envref.Spec
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
// hosts empty means "every host whose LiteSPM bridge verifies as registered" —
// a host the user never set LiteSPM up in is not one we edit on their behalf.
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

	// The launch line comes from the published version record's component
	// runtime, not from the listing: listings carry no command at all.
	if runtime == nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			"the catalog publishes no runnable command for this listing")
	}
	command := strings.TrimSpace(runtime.Command)
	if command == "" {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			"the published runtime descriptor has no command, so there is nothing to register")
	}
	name, err := serverEntryNameFor(listing)
	if err != nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID, err.Error())
	}
	if err := host.ValidateServerEntryName(name); err != nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID, err.Error())
	}

	if len(hosts) == 0 {
		hosts = host.RegisteredBridgeHosts(ctx, scope)
	}
	if len(hosts) == 0 {
		return nil, domain.ErrInstallTargetUnavailable(
			"no agent host has a verified LiteSPM bridge entry; run 'litespm host setup <host-id>' first, " +
				"or pass --host to name one explicitly")
	}

	entry := host.ServerEntry{
		Name:     name,
		Command:  command,
		Args:     append([]string{}, runtime.Args...),
		EnvNames: append([]string{}, envNames...),
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

	for _, hostID := range hosts {
		result, err := host.InstallServerEntry(ctx, hostID, entry, host.EntryInstallOptions{
			BackupDir: backupDir,
			Scope:     scope,
			Force:     force,
		})
		if err != nil {
			return nil, err
		}
		outcome.Hosts = append(outcome.Hosts, result)
	}

	outcome.InstallID = fmt.Sprintf("inst_%s_%s_%s", scope, safeInstallIDPart(listing.ID), safeInstallIDPart(version))
	rec := &domain.InstallRecord{
		InstallID:   outcome.InstallID,
		ListingID:   listing.ID,
		Version:     version,
		Scope:       scope,
		Status:      domain.InstallActive,
		InstalledAt: time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := db.SaveInstall(ctx, rec); err != nil {
		return nil, fmt.Errorf("record the install: %w", err)
	}
	// The provider row's component_id is a foreign key onto install_components
	// (ARCH/12 §13), so the component must exist. SaveInstallComponent keys that
	// table by install id, which is also what SaveProvider falls back to.
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{
		InstallID:     outcome.InstallID,
		Kind:          domain.ComponentMCPProvider,
		ComponentName: name,
		Path:          "",
	}); err != nil {
		return nil, fmt.Errorf("record the installed component: %w", err)
	}
	// One host_registrations row per config actually modified: this is the first
	// production caller of that table, so it is also the first evidence that it
	// records something real.
	for _, result := range outcome.Hosts {
		if err := db.SaveHostRegistration(ctx, &domain.HostRegistrationRecord{
			HostID:       result.HostID,
			Scope:        scope,
			ConfigPath:   result.ConfigPath,
			ConfigFormat: string(hostConfigFormatFor(result.HostID)),
			RegisteredAt: time.Now().UTC(),
			Status:       "active",
		}); err != nil {
			return nil, fmt.Errorf("record the host registration: %w", err)
		}
	}

	return outcome, nil
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
	fmt.Printf("✓ Registered MCP server %s\n", outcome.Entry.Name)
	fmt.Printf("  Listing ID:  %s\n", outcome.InstallID)
	fmt.Printf("  Command:     %s %s\n", outcome.Entry.Command, strings.Join(outcome.Entry.Args, " "))
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
	if outcome.ClaimedTransport != "" && outcome.ClaimedTransport != "stdio" {
		// A future release may publish a remote endpoint. Until one does, say so
		// rather than registering a local entry and implying otherwise.
		fmt.Printf("  Note: the catalog lists this server as %q but publishes no endpoint URL, so the\n"+
			"        local command above is what was registered.\n", outcome.ClaimedTransport)
	}
	printForwardedEnv(outcome)
	if len(outcome.Hosts) > 1 {
		fmt.Printf("  Note: the agent will spawn this server on next start; some agents need a restart.\n")
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
