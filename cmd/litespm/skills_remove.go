package main

// skills_remove.go — `litespm skills remove`.
//
// The counterpart to `skills add`. It reads the install ledger and deletes only
// the directories the ledger names, refusing anything that no longer looks like
// a skill we installed. `litespm uninstall` reports these directories rather
// than deleting them, because a skills tree may hold the user's own files; this
// command is the explicit, opt-in deletion.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/skills"
)

func skillsRemoveUsage() {
	fmt.Println(`Usage: litespm skills remove [<name> ...] [--all] [flags]

  <name>                    one or more skill names as recorded in the install ledger
  --all                     remove every matching skill this tool installed
  --scope <project|global>  restrict removal to this scope (default: project);
                            entries whose recorded scope differs are refused, not deleted
  --global, -g              shorthand for --scope global
  --force, -f               remove a directory even if it holds files added after install
  --dry-run, -n             show what would be removed without deleting anything
  --json                    machine-readable output
  --help, -h                show this help text

Run ` + "`litespm skills list`" + ` to see what the ledger holds.`)
}

func runSkillsList(args []string) {
	jsonOut := hasFlag(args, "--json")

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ledger, err := skills.OpenLedger(skills.LedgerPath(paths.DataRoot))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	entries, err := ledger.All()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		payload, _ := json.Marshal(map[string]any{"installed": entries})
		fmt.Println(string(payload))
		return
	}
	if len(entries) == 0 {
		fmt.Println("No skills have been installed by this tool.")
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SkillName != entries[j].SkillName {
			return entries[i].SkillName < entries[j].SkillName
		}
		return entries[i].DestDir < entries[j].DestDir
	})

	fmt.Printf("Skills installed by this tool (%d director%s):\n\n",
		len(entries), pluralY(len(entries)))
	fmt.Printf("%-24s %-16s %s\n", "SKILL", "HOST", "DIRECTORY")
	fmt.Println(repeat("-", 78))
	for _, e := range entries {
		fmt.Printf("%-24s %-16s %s\n", e.SkillName, e.HostLabel, e.DestDir)
	}
	fmt.Println()
	fmt.Println("Remove with: litespm skills remove <name>  or  litespm skills remove --all")
}

type skillsRemoveOptions struct {
	names      []string
	all        bool
	dryRun     bool
	jsonOut    bool
	scope      string
	scopeGiven bool
	force      bool
	help       bool
}

func parseSkillsRemoveArgs(args []string) (skillsRemoveOptions, error) {
	var opts skillsRemoveOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", a)
			}
			i++
			return args[i], nil
		}
		switch a {
		case "--all":
			opts.all = true
		case "--dry-run", "-n":
			opts.dryRun = true
		case "--json":
			opts.jsonOut = true
		case "--force", "-f":
			opts.force = true
		case "--scope":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.scope = strings.ToLower(v)
			opts.scopeGiven = true
		case "--global", "-g":
			opts.scope = "global"
			opts.scopeGiven = true
		case "--help", "-h":
			opts.help = true
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %s", a)
			}
			opts.names = append(opts.names, a)
		}
	}
	if opts.scopeGiven && opts.scope != "project" && opts.scope != "global" {
		return opts, fmt.Errorf("invalid scope %q: want project|global", opts.scope)
	}
	if !opts.scopeGiven {
		// Removal is scope-checked: without an explicit scope the command acts
		// on project-scoped installs, mirroring `skills add`. Global entries are
		// left in place and reported, never deleted across scopes silently.
		opts.scope = "project"
	}
	return opts, nil
}

func runSkillsRemove(args []string) {
	opts, err := parseSkillsRemoveArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		skillsRemoveUsage()
		os.Exit(1)
	}
	if opts.help {
		skillsRemoveUsage()
		return
	}
	if !opts.all && len(opts.names) == 0 {
		skillsRemoveUsage()
		os.Exit(1)
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	outcomes, err := executeSkillsRemove(opts, paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var removed, kept int
	for _, o := range outcomes {
		if o.Removed {
			removed++
		} else {
			kept++
		}
	}

	if opts.jsonOut {
		payload, _ := json.Marshal(map[string]any{
			"dryRun":  opts.dryRun,
			"removed": removed,
			"kept":    kept,
			"outcomes": func() []map[string]string {
				out := make([]map[string]string, 0, len(outcomes))
				for _, o := range outcomes {
					out = append(out, map[string]string{
						"skill":  o.Entry.SkillName,
						"host":   o.Entry.HostLabel,
						"dir":    o.Entry.DestDir,
						"reason": o.Reason,
					})
				}
				return out
			}(),
		})
		fmt.Println(string(payload))
		return
	}

	if len(outcomes) == 0 {
		fmt.Println("Nothing to remove: the ledger has no matching entries.")
		return
	}

	for _, o := range outcomes {
		switch {
		case o.Removed && opts.dryRun:
			fmt.Printf("  - %-20s would remove %s\n", o.Entry.SkillName, o.Entry.DestDir)
		case o.Removed:
			fmt.Printf("  ✓ %-20s removed %s\n", o.Entry.SkillName, o.Entry.DestDir)
		default:
			fmt.Printf("  ○ %-20s left in place: %s\n", o.Entry.SkillName, o.Reason)
		}
	}

	fmt.Println()
	if opts.dryRun {
		fmt.Printf("Dry run: %d director%s would be removed, %d left in place.\n",
			removed, pluralY(removed), kept)
		fmt.Println("Re-run without --dry-run to apply.")
		return
	}
	fmt.Printf("Done: %d removed, %d left in place.\n", removed, kept)
}

// executeSkillsRemove resolves the requested scope and delegates to the
// ledger's scope-checked removal. An empty scope means "no scope filter";
// a --scope is passed through so a cross-scope --all cannot delete silently.
func executeSkillsRemove(opts skillsRemoveOptions, paths *config.PlatformPaths) ([]skills.RemovalOutcome, error) {
	ledger, err := skills.OpenLedger(skills.LedgerPath(paths.DataRoot))
	if err != nil {
		return nil, err
	}
	var names []string
	if !opts.all {
		names = opts.names
	}
	// Removal is always scope-checked. parseSkillsRemoveArgs resolves the
	// default (project); resolving it here too keeps the guarantee for callers
	// that build options directly. A cross-scope --all is refused per entry
	// rather than deleting silently.
	scope := opts.scope
	if scope == "" {
		scope = "project"
	}
	return ledger.RemoveScoped(skills.RemoveOptions{
		Names:  names,
		Scope:  scope,
		DryRun: opts.dryRun,
		Force:  opts.force,
	})
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
