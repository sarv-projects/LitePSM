package main

// skills_remove.go — `litepsm skills remove`.
//
// The counterpart to `skills add`. It reads the install ledger and deletes only
// the directories the ledger names, refusing anything that no longer looks like
// a skill we installed. `litepsm uninstall` reports these directories rather
// than deleting them, because a skills tree may hold the user's own files; this
// command is the explicit, opt-in deletion.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/skills"
)

func skillsRemoveUsage() {
	fmt.Println("Usage: litepsm skills remove [<name> ...] [--all] [--dry-run] [--json]")
	fmt.Println()
	fmt.Println("  <name>      one or more skill names as recorded in the install ledger")
	fmt.Println("  --all       remove every skill this tool installed")
	fmt.Println("  --dry-run   show what would be removed without deleting anything")
	fmt.Println("  --json      machine-readable output")
	fmt.Println()
	fmt.Println("Run `litepsm skills list` to see what the ledger holds.")
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
	fmt.Println("Remove with: litepsm skills remove <name>  or  litepsm skills remove --all")
}

func runSkillsRemove(args []string) {
	jsonOut := hasFlag(args, "--json")
	dryRun := hasFlag(args, "--dry-run")
	all := hasFlag(args, "--all")

	var names []string
	for _, a := range args {
		if len(a) > 0 && a[0] != '-' {
			names = append(names, a)
		}
	}
	if !all && len(names) == 0 {
		skillsRemoveUsage()
		os.Exit(1)
	}
	if all {
		names = nil // Remove(nil) means "everything in the ledger"
	}

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

	outcomes, err := ledger.Remove(names, dryRun)
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

	if jsonOut {
		payload, _ := json.Marshal(map[string]any{
			"dryRun":  dryRun,
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
		case o.Removed && dryRun:
			fmt.Printf("  - %-20s would remove %s\n", o.Entry.SkillName, o.Entry.DestDir)
		case o.Removed:
			fmt.Printf("  ✓ %-20s removed %s\n", o.Entry.SkillName, o.Entry.DestDir)
		default:
			fmt.Printf("  ○ %-20s left in place: %s\n", o.Entry.SkillName, o.Reason)
		}
	}

	fmt.Println()
	if dryRun {
		fmt.Printf("Dry run: %d director%s would be removed, %d left in place.\n",
			removed, pluralY(removed), kept)
		fmt.Println("Re-run without --dry-run to apply.")
		return
	}
	fmt.Printf("Done: %d removed, %d left in place.\n", removed, kept)
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
