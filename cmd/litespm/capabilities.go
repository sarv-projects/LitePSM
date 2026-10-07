package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/discover"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/state"
)

// capabilitiesUsage documents the subcommands inline: this is a new surface and
// its failure modes (nothing discovered yet, a server that will not start) are
// things a user will hit on the first try.
const capabilitiesUsage = `Usage: litespm capabilities <command> [flags]

Commands:
  list        List the tools discovered from installed MCP servers
  search      Search discovered tools by name or description
  describe    Show one discovered tool's input schema and the command behind it
  refresh     Re-probe every installed MCP server and update its tool list

Flags:
  --host <id>   Only this agent host (repeatable); default: every configured host
  --capability <id>   The capability to describe
  --limit <n>   Maximum rows to print (default 50)`

// runCapabilities dispatches the `litespm capabilities` commands.
func runCapabilities(args []string) {
	if len(args) == 0 {
		fmt.Println(capabilitiesUsage)
		os.Exit(2)
	}
	command := args[0]
	rest := args[1:]

	hosts, limit, capabilityID, err := parseCapabilityFlags(rest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n%s\n", err, capabilitiesUsage)
		os.Exit(2)
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open state database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	switch command {
	case "refresh":
		client, cerr := newCatalogClient(paths)
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "Failed to open the catalog index: %v\n", cerr)
			fmt.Fprintln(os.Stderr, "Run 'litespm catalog sync' first.")
			os.Exit(1)
		}
		refreshCapabilities(ctx, db, client, hosts)
	case "list", "search":
		query := ""
		if command == "search" {
			if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
				fmt.Fprintf(os.Stderr, "search needs a query\n\n%s\n", capabilitiesUsage)
				os.Exit(2)
			}
			query = rest[0]
		}
		listCapabilities(ctx, db, query, limit)
	case "describe":
		if capabilityID == "" {
			fmt.Fprintf(os.Stderr, "describe needs --capability <id>\n\n%s\n", capabilitiesUsage)
			os.Exit(2)
		}
		describeCapability(ctx, db, capabilityID)
	case "help", "--help", "-h":
		fmt.Println(capabilitiesUsage)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q\n\n%s\n", command, capabilitiesUsage)
		os.Exit(2)
	}
}

func parseCapabilityFlags(args []string) (hosts []string, limit int, capabilityID string, err error) {
	limit = 50
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			if i+1 >= len(args) {
				return nil, 0, "", fmt.Errorf("flag --host requires a value")
			}
			i++
			hosts = append(hosts, args[i])
		case "--capability":
			if i+1 >= len(args) {
				return nil, 0, "", fmt.Errorf("flag --capability requires a value")
			}
			i++
			capabilityID = args[i]
		case "--limit":
			if i+1 >= len(args) {
				return nil, 0, "", fmt.Errorf("flag --limit requires a value")
			}
			i++
			if _, scanErr := fmt.Sscanf(args[i], "%d", &limit); scanErr != nil || limit <= 0 {
				return nil, 0, "", fmt.Errorf("--limit must be a positive number")
			}
		case "-h", "--help":
			return nil, 0, "", nil
		default:
			// A bare word is the search query; leave it for the caller.
			return hosts, limit, capabilityID, nil
		}
	}
	return hosts, limit, capabilityID, nil
}

// installedMCPServers resolves every configured host's registered servers into
// probe specs, pairing each with the install record it belongs to.
func installedMCPServers(ctx context.Context, db *state.DB, client *catalog.Client, hosts []string) ([]discover.ProviderSpec, error) {
	if len(hosts) == 0 {
		hosts = host.RegisteredBridgeHosts(ctx, domain.ScopeUser)
	}
	if len(hosts) == 0 {
		return nil, domain.ErrInstallTargetUnavailable(
			"no agent host has a verified LiteSPM bridge entry; run 'litespm host setup <host-id>' first, " +
				"or pass --host to name one explicitly")
	}

	// A host config entry carries only a NAME, because that is all the host
	// schemas allow — so the entry is matched back to its install by recomputing
	// the same normalized name the installer derived from the listing. Recomputing
	// from the same listing is reproducible; guessing from the install id alone
	// would not be, because a listing's name and its id's last segment differ.
	installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
	if err != nil {
		return nil, err
	}
	byEntryName := map[string]string{}
	for _, rec := range installs {
		if rec.Status != domain.InstallActive {
			continue
		}
		listing, lerr := client.GetListing(rec.ListingID)
		if lerr != nil || listing.Kind != domain.KindMCP {
			continue
		}
		name, nerr := serverEntryNameFor(listing)
		if nerr != nil {
			continue
		}
		if _, seen := byEntryName[name]; !seen {
			byEntryName[name] = rec.InstallID
		}
	}

	var specs []discover.ProviderSpec
	for _, hostID := range hosts {
		entries, listErr := host.ListServerEntriesWithValues(ctx, hostID, domain.ScopeUser)
		if listErr != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", hostID, listErr)
			continue
		}
		for _, entry := range entries {
			installID, known := byEntryName[entry.Name]
			if !known {
				// Not installed by LiteSPM (a server the user configured by hand):
				// not ours to probe, record, or later invoke.
				continue
			}
			specs = append(specs, discover.ProviderSpec{
				InstallID:     installID,
				ComponentName: entry.Name,
				Command:       entry.Command,
				Args:          entry.Args,
				Env:           entry.Env,
				HostID:        hostID,
				Transport:     entry.Transport,
			})
		}
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no MCP servers installed by LiteSPM were found on the selected hosts; " +
			"install one with 'litespm install <mcp-id>' first")
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].ComponentName != specs[j].ComponentName {
			return specs[i].ComponentName < specs[j].ComponentName
		}
		return specs[i].InstallID < specs[j].InstallID
	})
	return specs, nil
}

func refreshCapabilities(ctx context.Context, db *state.DB, client *catalog.Client, hosts []string) {
	specs, err := installedMCPServers(ctx, db, client, hosts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Capability refresh failed: %v\n", err)
		os.Exit(1)
	}
	total, failed := 0, 0
	for _, spec := range specs {
		fmt.Printf("Probing %s (%s %s)...\n", spec.ComponentName, spec.Command, strings.Join(spec.Args, " "))
		found, derr := discover.Discover(ctx, db, spec)
		if derr != nil {
			// One unstartable server must not abandon the others: the operator
			// needs to know which of N servers is broken, not that N failed.
			failed++
			fmt.Printf("  ✗ %s: %v\n", spec.ComponentName, derr)
			continue
		}
		total += len(found.Capabilities)
		names := make([]string, 0, len(found.Capabilities))
		for _, c := range found.Capabilities {
			names = append(names, c.Name)
		}
		fmt.Printf("  ✓ %d tools: %s\n", len(found.Capabilities), strings.Join(names, ", "))
	}
	fmt.Printf("\n✓ Discovered %d tools across %d servers", total, len(specs)-failed)
	if failed > 0 {
		fmt.Printf(" (%d server(s) could not be probed)", failed)
	}
	fmt.Println()
	if failed > 0 {
		os.Exit(1)
	}
}

func listCapabilities(ctx context.Context, db *state.DB, query string, limit int) {
	rows, err := db.ListCapabilities(ctx, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list capabilities: %v\n", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("No tools discovered yet. Run 'litespm capabilities refresh'.")
		return
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	shown := 0
	for _, row := range rows {
		if needle != "" &&
			!strings.Contains(strings.ToLower(row.Name), needle) &&
			!strings.Contains(strings.ToLower(row.Description), needle) {
			continue
		}
		if shown >= limit {
			break
		}
		shown++
		fmt.Printf("%s\n  %s\n  provider %s\n", row.CapabilityID, row.Description, row.ProviderID)
	}
	fmt.Printf("\n%d of %d discovered tools\n", shown, len(rows))
}

func describeCapability(ctx context.Context, db *state.DB, capabilityID string) {
	record, err := db.GetCapability(ctx, capabilityID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No discovered capability %q. Run 'litespm capabilities refresh'.\n", capabilityID)
		os.Exit(1)
	}
	provider, perr := db.GetProvider(ctx, record.ProviderID)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "Provider %s is not registered: %v\n", record.ProviderID, perr)
		os.Exit(1)
	}
	fmt.Printf("Capability: %s\n", record.CapabilityID)
	fmt.Printf("  Name:        %s\n", record.Name)
	if record.Description != "" {
		fmt.Printf("  Description: %s\n", record.Description)
	}
	fmt.Printf("  Provider:    %s (%s)\n", record.ProviderID, provider.Transport)
	fmt.Printf("  Command:     %s %s\n", provider.Command, provider.ArgsJSON)
	fmt.Printf("  Fingerprint: %s\n", record.SchemaFingerprint)
	fmt.Printf("  Discovered:  %s\n\n", record.DiscoveredAt.Format("2006-01-02T15:04:05Z"))
	fmt.Printf("Input schema:\n%s\n", record.InputSchemaJSON)
	fmt.Printf("\nCall it with: litespm invoke %s --arguments '{\"...\":\"...\"}'\n", record.CapabilityID)
}

// runInvoke calls one discovered capability from the terminal. It is the same
// path the Bridge uses (`provider.invoke` -> discover.Invoke), so a capability
// cannot behave differently depending on who called it.
func runInvoke(args []string) {
	usage := "Usage: litespm invoke <capability-id> [--arguments '<json>']"
	if len(args) == 0 {
		fmt.Println(usage)
		os.Exit(2)
	}
	capabilityID := args[0]
	arguments := json.RawMessage("{}")
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--arguments", "-a":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "flag %s requires a value\n", args[i])
				os.Exit(2)
			}
			i++
			if !json.Valid([]byte(args[i])) {
				fmt.Fprintf(os.Stderr, "--arguments must be valid JSON, got: %s\n", args[i])
				os.Exit(2)
			}
			arguments = json.RawMessage(args[i])
		default:
			fmt.Fprintf(os.Stderr, "Unknown argument %q\n\n%s\n", args[i], usage)
			os.Exit(2)
		}
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open state database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	// The same policy gate as the daemon's provider.invoke (ARCH/15): without
	// an engine discover.Invoke refuses every effectful invocation, so the CLI
	// builds one from configuration and the user's rules. Failing to load the
	// engine is a refusal, never a bypass.
	policyEngine, closePolicy, perr := openCLIPolicyEngine(paths.DataRoot)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "Invoke refused: %v\n", perr)
		os.Exit(1)
	}
	defer closePolicy()
	result, err := discover.Invoke(ctx, db, capabilityID, arguments, discover.WithPolicy(policyEngine))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invoke failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(result.Output)
	if !strings.HasSuffix(result.Output, "\n") {
		fmt.Println()
	}
	if result.IsError {
		os.Exit(1)
	}
}

// newCatalogClient opens the local catalog index against the configured registry.
func newCatalogClient(paths *config.PlatformPaths) (*catalog.Client, error) {
	cfg, _ := config.LoadCurrentConfig()
	registry := registryURLOf(cfg)
	client := catalog.NewClientWithTimeout(registry, paths.DataRoot, catalogTimeoutOf(cfg), nil)
	if err := client.LoadFromCache(); err != nil {
		return nil, err
	}
	return client, nil
}
