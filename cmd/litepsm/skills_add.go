package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/skills"
)

const litepsmBanner = `
██╗     ██╗████████╗███████╗██████╗ ███████╗███╗   ███╗
██║     ██║╚══██╔══╝██╔════╝██╔══██╗██╔════╝████╗ ████║
██║     ██║   ██║   █████╗  ██████╔╝███████╗██╔████╔██║
██║     ██║   ██║   ██╔══╝  ██╔═══╝ ╚════██║██║╚██╔╝██║
███████╗██║   ██║   ███████╗██║     ███████║██║ ╚═╝ ██║
╚══════╝╚═╝   ╚═╝   ╚══════╝╚═╝     ╚══════╝╚═╝     ╚═╝`

func skillsAddUsage() {
	fmt.Println(`Usage: litepsm skills add <source> [flags]

Install portable SKILL.md skills from a GitHub repository or local directory.

  <source>                  owner/repo, https git URL, or local directory path

Flags:
  --skill <name>            Select a skill (repeatable; defaults to interactive)
  --agent <host-id>         Target an agent host (repeatable; defaults to interactive)
  --global, -g              Install user-global (default: this repo only)
  --scope <project|global>  Explicit install scope
  --list                    List discovered skills without installing
  --yes, -y                 Non-interactive: install all discovered skills
  --json                    Machine-readable summary on stdout

Each selected skill is copied into every selected agent's own skill
directory: repo-local for project scope, or the user-global dir for global
scope (honours CODEX_HOME, CLAUDE_CONFIG_DIR, GROK_HOME and XDG_CONFIG_HOME).
Hosts with no dedicated dir (cline, pi-agent) share the universal
.agents/skills tree. Risk data is not collected: every skill is reported
unverified until a real audit source exists.`)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

type skillsAddOptions struct {
	source     string
	skills     []string
	agents     []string
	global     bool
	scope      string
	scopeGiven bool
	list       bool
	yes        bool
	jsonOut    bool
}

func parseSkillsAddArgs(args []string) (skillsAddOptions, error) {
	var opts skillsAddOptions
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
		case "--skill":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.skills = append(opts.skills, v)
		case "--agent", "-a":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.agents = append(opts.agents, strings.ToLower(v))
		case "--global", "-g":
			opts.global = true
		case "--scope":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.scope = strings.ToLower(v)
			opts.scopeGiven = true
		case "--list":
			opts.list = true
		case "--yes", "-y":
			opts.yes = true
		case "--json":
			opts.jsonOut = true
		case "--help", "-h":
			skillsAddUsage()
			os.Exit(0)
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %s", a)
			}
			if opts.source != "" {
				return opts, fmt.Errorf("unexpected argument %s", a)
			}
			opts.source = a
		}
	}
	if opts.source == "" {
		return opts, fmt.Errorf("missing <source>")
	}
	if opts.scope == "" {
		if opts.global {
			opts.scope = "global"
		} else {
			opts.scope = "project"
		}
	}
	if opts.scope != "project" && opts.scope != "global" {
		return opts, fmt.Errorf("invalid scope %q: want project|global", opts.scope)
	}
	return opts, nil
}

func runSkillsAdd(args []string) {
	opts, err := parseSkillsAddArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		skillsAddUsage()
		os.Exit(1)
	}

	interactive := isTerminal(os.Stdin) && !opts.yes && !opts.jsonOut

	home, _ := os.UserHomeDir()
	project, _ := os.Getwd()

	platformPaths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving platform paths: %v\n", err)
		os.Exit(1)
	}

	src, err := skills.ParseSkillSource(opts.source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var workRoot string
	var cleanup func()
	if src.Kind == "local" {
		workRoot = src.LocalDir
	} else {
		if _, err := exec.LookPath("git"); err != nil {
			fmt.Fprintln(os.Stderr, "Error: git is required to fetch remote skill sources")
			os.Exit(1)
		}
		tmp, err := os.MkdirTemp("", "litepsm-skills-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cleanup = func() { os.RemoveAll(tmp) }
		defer cleanup()
		clone := exec.Command("git", "clone", "--depth", "1", src.CloneURL, tmp)
		if out, err := clone.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to clone %s: %v\n%s\n", src.Display, err, strings.TrimSpace(string(out)))
			os.Exit(1)
		}
		workRoot = tmp
	}

	found, err := skills.DiscoverSkills(workRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(found) == 0 {
		fmt.Fprintln(os.Stderr, "No installable skills found (need SKILL.md with name and description).")
		os.Exit(1)
	}

	if opts.list {
		fmt.Printf("Skills in %s (%d):\n\n", src.Display, len(found))
		for _, f := range found {
			fmt.Printf("  %-28s %s\n", f.Pkg.Name, f.Pkg.Description)
		}
		return
	}

	// --- Skill selection ---
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	in := bufio.NewReader(os.Stdin)

	askLine := func() string {
		line, _ := in.ReadString('\n')
		return strings.TrimSpace(line)
	}

	if !opts.jsonOut {
		fmt.Fprintln(out, litepsmBanner)
		fmt.Fprintln(out, "\n┌   skills")
		fmt.Fprintf(out, "│\n◇  Source: %s\n│\n", src.Display)
		if src.Kind == "local" {
			fmt.Fprintln(out, "◇  Local directory")
		} else {
			fmt.Fprintln(out, "◇  Repository cloned")
		}
		fmt.Fprintln(out, "│")
		fmt.Fprintf(out, "◇  Found %d skill%s\n", len(found), plural(len(found)))
	}

	var selected []skills.DiscoveredSkill
	if len(opts.skills) > 0 {
		want := map[string]bool{}
		for _, s := range opts.skills {
			want[strings.ToLower(s)] = true
		}
		var missing []string
		for _, f := range found {
			if want[strings.ToLower(f.Pkg.Name)] {
				selected = append(selected, f)
				delete(want, strings.ToLower(f.Pkg.Name))
			}
		}
		for m := range want {
			missing = append(missing, m)
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "Error: unknown skill(s): %s\nAvailable:", strings.Join(missing, ", "))
			for _, f := range found {
				fmt.Fprintf(os.Stderr, " %s", f.Pkg.Name)
			}
			fmt.Fprintln(os.Stderr)
			os.Exit(1)
		}
	} else if len(found) == 1 || opts.yes || !interactive {
		if !interactive && len(found) > 1 && !opts.yes {
			fmt.Fprintln(os.Stderr, "Error: multiple skills found; re-run with --skill <name> (repeatable) or --yes in non-interactive mode.")
			os.Exit(1)
		}
		selected = found
	}

	// --- Scope selection (global, or this repo only) ---
	if interactive && !opts.scopeGiven && !opts.global {
		fmt.Fprintln(out, "◇  Installation scope")
		fmt.Fprintln(out, "│  1) This repo only")
		fmt.Fprintln(out, "│  2) Global (user-wide)")
		fmt.Fprint(out, "│  > ")
		out.Flush()
		switch askLine() {
		case "2", "global":
			opts.scope = "global"
		default:
			opts.scope = "project"
		}
	}
	if !opts.jsonOut {
		fmt.Fprintln(out, "│")
		fmt.Fprintf(out, "◇  Scope: %s\n", scopeLabel(opts.scope))
	}

	// --- Agent selection ---
	targets := skills.AgentTargets()
	knownAgents := map[string]string{}
	for _, a := range targets {
		knownAgents[a.ID] = a.DisplayName
	}
	var agents []string
	if len(opts.agents) > 0 {
		for _, ag := range opts.agents {
			if _, ok := knownAgents[ag]; !ok {
				fmt.Fprintf(os.Stderr, "Error: unknown agent %q\nKnown agents: %s\n", ag, strings.Join(skills.AgentIDs(), " "))
				os.Exit(1)
			}
			agents = append(agents, ag)
		}
	}
	if len(agents) == 0 && !interactive {
		fmt.Fprintln(os.Stderr, "Error: no agents selected; re-run with --agent <host-id> (repeatable) in non-interactive mode.")
		os.Exit(1)
	}

	if len(selected) == 0 && interactive {
		fmt.Fprintln(out, "◇  Select skills to install (numbers, comma-separated, or 'all')")
		for i, f := range found {
			fmt.Fprintf(out, "│  %d) %s — %s\n", i+1, f.Pkg.Name, truncateRunes(f.Pkg.Description, 80))
		}
		fmt.Fprint(out, "│  > ")
		out.Flush()
		answer := askLine()
		picked, err := parseNumberSelection(answer, len(found))
		if err != nil || len(picked) == 0 {
			fmt.Fprintln(os.Stderr, "Error: no skills selected.")
			os.Exit(1)
		}
		for _, idx := range picked {
			selected = append(selected, found[idx])
		}
	} else if len(selected) == 0 {
		selected = found
	}

	if len(opts.agents) == 0 && interactive {
		// Detected agents first (installed on this machine), then the rest.
		// Popularity is NOT used to order agents: we have no measured usage
		// data, so this is presence-first then alphabetical.
		var detectedTargets, otherTargets []skills.AgentTarget
		for _, a := range targets {
			if skills.AgentInstalled(a, project, home) {
				detectedTargets = append(detectedTargets, a)
			} else {
				otherTargets = append(otherTargets, a)
			}
		}
		fmt.Fprintf(out, "◇  %d agents (%d detected)\n", len(targets), len(detectedTargets))
		fmt.Fprintln(out, "◇  Which agents do you want to install to?")
		ids := make([]string, 0, len(targets))
		var detectedIDs []string
		i := 1
		for _, group := range []struct {
			label string
			list  []skills.AgentTarget
		}{{"Detected", detectedTargets}, {"All agents", otherTargets}} {
			if len(group.list) == 0 {
				continue
			}
			if group.label == "All agents" {
				fmt.Fprintln(out, "│")
			}
			fmt.Fprintf(out, "◇  %s\n", group.label)
			for _, a := range group.list {
				dir, ok := skills.AgentSkillDir(a.ID, opts.scope, project, home)
				pathText := "no global location"
				if ok {
					pathText = shortHome(dir, home)
				} else if opts.scope != "global" {
					pathText = "—"
				}
				mark := " "
				if a.Universal {
					mark = "◦"
				}
				fmt.Fprintf(out, "│  %d) [%s] %-22s %s\n", i, mark, a.DisplayName, pathText)
				ids = append(ids, a.ID)
				if group.label == "Detected" {
					detectedIDs = append(detectedIDs, a.ID)
				}
				i++
			}
		}
		fmt.Fprintln(out, "│  [ ] needs its own dir   [◦] shares .agents/skills")
		fmt.Fprint(out, "│  numbers, comma-separated, or empty for detected > ")
		out.Flush()
		answer := strings.TrimSpace(askLine())
		if answer == "" {
			agents = detectedIDs
		} else {
			picked, err := parseNumberSelection(answer, len(ids))
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error: invalid agent selection.")
				os.Exit(1)
			}
			for _, idx := range picked {
				agents = append(agents, ids[idx])
			}
		}
		if len(agents) == 0 {
			fmt.Fprintln(os.Stderr, "Error: no agents selected and none detected.")
			os.Exit(1)
		}
	}

	ops := skills.PlanInstall(selected, agents, opts.scope, project, home)
	if len(ops) == 0 {
		fmt.Fprintln(os.Stderr, "Error: nothing to install.")
		os.Exit(1)
	}

	if !interactive && !opts.yes {
		fmt.Fprintln(os.Stderr, "Error: refusing to install non-interactively without --yes.")
		os.Exit(1)
	}

	proceed := opts.yes
	if !opts.jsonOut {
		printInstallSummary(out, src, selected, ops)
		printRiskPanel(out, src, selected)
		if interactive {
			fmt.Fprint(out, "◇  Proceed with installation? [y/N] > ")
			out.Flush()
			answer := strings.ToLower(askLine())
			proceed = answer == "y" || answer == "yes"
		} else {
			proceed = true
		}
		if !proceed {
			fmt.Fprintln(out, "Cancelled.")
			return
		}
		fmt.Fprintln(out, "◇  Installation complete")
		fmt.Fprintln(out, "│")
	}

	// --- Install ---
	//
	// Every directory we create is recorded in the ledger. Without it the tool
	// could write into a dozen agent trees and have no way to enumerate them
	// again, which would make removal impossible.
	ledger, ledgerErr := skills.OpenLedger(skills.LedgerPath(platformPaths.DataRoot))
	if ledgerErr != nil {
		fmt.Fprintf(os.Stderr, "Error opening the install ledger: %v\n", ledgerErr)
		os.Exit(1)
	}

	var done []installedSkill
	var recorded []skills.LedgerEntry
	for _, op := range ops {
		if err := skills.CopySkillDir(op.FromDir, op.ToDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error installing %s to %s: %v\n", op.SkillName, op.ToDir, err)
			os.Exit(1)
		}
		done = append(done, installedSkill{Skill: op.SkillName, To: op.ToDir, Host: op.HostLabel})
		recorded = append(recorded, skills.LedgerEntry{
			SkillName: op.SkillName,
			AgentID:   op.HostLabel,
			HostLabel: op.HostLabel,
			DestDir:   op.ToDir,
			Source:    src.Display,
			Scope:     opts.scope,
		})
	}
	if err := ledger.Add(recorded); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: skills were installed but the ledger could not be written: %v\n", err)
		fmt.Fprintln(os.Stderr, "         `litepsm skills remove` will not be able to find them.")
	}

	if opts.jsonOut {
		payload, _ := json.Marshal(map[string]any{
			"source":    src.Display,
			"scope":     opts.scope,
			"installed": done,
		})
		fmt.Println(string(payload))
		return
	}

	printCompletion(out, src, selected, done, home)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// parseNumberSelection parses "1,3" or "all" into zero-based indexes.
func parseNumberSelection(answer string, total int) ([]int, error) {
	a := strings.ToLower(strings.TrimSpace(answer))
	if a == "all" || a == "*" {
		idx := make([]int, total)
		for i := range idx {
			idx[i] = i
		}
		return idx, nil
	}
	var out []int
	seen := map[int]bool{}
	for _, part := range strings.Split(a, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > total {
			return nil, fmt.Errorf("invalid selection %q", part)
		}
		if !seen[n-1] {
			seen[n-1] = true
			out = append(out, n-1)
		}
	}
	return out, nil
}

// shortHome renders a path with the home directory collapsed to "~".
func shortHome(path, home string) string {
	if home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+string(filepath.Separator)) {
			return "~" + path[len(home):]
		}
	}
	return path
}

func scopeLabel(scope string) string {
	if scope == "global" {
		return "Global (user-wide)"
	}
	return "This repo only"
}

func printInstallSummary(out *bufio.Writer, src skills.SkillSource, selected []skills.DiscoveredSkill, ops []skills.InstallOp) {
	fmt.Fprintln(out, "◇  Installation Summary ──────────────────────────────╮")
	fmt.Fprintln(out, "│")
	title := src.RepoPath
	if title == "" {
		title = src.Display
	}
	fmt.Fprintf(out, "│  %s\n│\n", truncateRunes(title, 55))

	// One row per (skill, target dir), grouped by skill.
	type target struct{ dir, host string }
	order := []string{}
	bySkill := map[string][]target{}
	for _, op := range ops {
		if _, ok := bySkill[op.SkillName]; !ok {
			order = append(order, op.SkillName)
		}
		bySkill[op.SkillName] = append(bySkill[op.SkillName], target{dir: op.ToDir, host: op.HostLabel})
	}
	for _, name := range order {
		fmt.Fprintf(out, "│  %s\n", name)
		for _, t := range bySkill[name] {
			fmt.Fprintf(out, "│    → %s\n", t.dir)
		}
	}
	fmt.Fprintln(out, "│")
	fmt.Fprintln(out, "├─────────────────────────────────────────────────────╯")
	fmt.Fprintln(out, "│")
}

func printRiskPanel(out *bufio.Writer, src skills.SkillSource, selected []skills.DiscoveredSkill) {
	fmt.Fprintln(out, "◇  Security Risk Assessment ──────────────────────────╮")
	fmt.Fprintln(out, "│")
	fmt.Fprintln(out, "│  skill                    verdict")
	for _, f := range selected {
		name, _ := skills.SanitizeSkillName(f.Pkg.Name)
		fmt.Fprintf(out, "│  %-25s unverified\n", truncateRunes(name, 25))
	}
	fmt.Fprintln(out, "│")
	if src.RepoPath != "" {
		fmt.Fprintf(out, "│  Details: https://skills.sh/%s\n", src.RepoPath)
	} else {
		fmt.Fprintln(out, "│  Audit lookup supports public GitHub repos only.")
	}
	fmt.Fprintln(out, "│  No audit data collected: review skills before use.")
	fmt.Fprintln(out, "├─────────────────────────────────────────────────────╯")
	fmt.Fprintln(out, "│")
}

type installedSkill struct {
	Skill string `json:"skill"`
	To    string `json:"to"`
	Host  string `json:"host"`
}

func printCompletion(out *bufio.Writer, src skills.SkillSource, selected []skills.DiscoveredSkill, done []installedSkill, home string) {
	fmt.Fprintf(out, "◇  Installed %d skill%s ────────────────────╮\n", len(selected), plural(len(selected)))
	fmt.Fprintln(out, "│")
	title := src.RepoPath
	if title == "" {
		title = src.Display
	}
	fmt.Fprintf(out, "│  %s\n", truncateRunes(title, 55))
	seen := map[string]bool{}
	for _, d := range done {
		if seen[d.Skill] {
			continue
		}
		seen[d.Skill] = true
		fmt.Fprintf(out, "│  ✓ %s (copied)\n", d.Skill)
		fmt.Fprintf(out, "│    → %s\n", shortHome(d.To, home))
	}
	if extra := len(done) - len(seen); extra > 0 {
		fmt.Fprintf(out, "│  (+%d more agent destination%s)\n", extra, plural(extra))
	}
	fmt.Fprintln(out, "│")
	fmt.Fprintln(out, "├────────────────────────────────────────╯")
	fmt.Fprintln(out, "│")
	fmt.Fprintln(out, "└  Done!  Review skills before use; they run with full agent permissions.")
}
