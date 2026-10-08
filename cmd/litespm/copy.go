package main

// copy.go — `litespm copy`: cross-agent porting (ARCH/38 §5, D-028).
//
// Copy reproduces one agent's setup in another through ONE canonical IR:
// N readers → host.ServerEntry / skill records → N writers. There is no
// pairwise translator anywhere in this file; shape variation lives as data in
// internal/host, and the planner that classifies everything lives in
// internal/porting with no I/O at all.
//
// The pipeline, in the only order it may run:
//
//	read source → normalize to IR → filter → support check → translate
//	→ conflict detection → PLAN printed (no writes happened)
//	→ approval (TTY "yes" | --apply --yes) → managed write through the
//	normal install paths (host-entry registration, skills ledger,
//	deployment rows, install transaction) → read-back verify (L1/L2)
//	→ per-item report + exit code.
//
// Three rules this file exists to keep:
//
//  1. Plan before write. The plan prints unconditionally, before any write,
//     and `--dry-run` is always accepted. Nothing in the read or plan phase
//     opens the state database or touches a config.
//  2. Secrets move as names and references, never values. The planner never
//     sees a literal (internal/porting/secrets.go drops them at normalize),
//     so no plan line and no written config can carry one — only Needs names.
//  3. Copies are managed, not spliced. Every write goes through the same
//     functions installs use, so `install remove` / `restore` reverse a copy
//     exactly as they reverse an install.
//
// Exit codes follow ARCH/20: 0 success (including "nothing to do"),
// 1 refused/failed, 2 usage. LPSM-COPY-001..007 are ARCH/38 §5.7.

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/lifecycle"
	"github.com/sarv-projects/litespm/internal/porting"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

const copyUsage = `Usage: litespm copy [items|kinds...] --from <host> --to <host> [flags]

Port an agent's MCP servers and skills into another agent: read the source
config through its adapter, normalize to the canonical IR, and write it
through the target's normal install path (ARCH/38 §5). The source is never
modified. The plan always prints before anything is written.

Flags:
  --from <host>             source agent id (required; see 'litespm host list')
  --to <host>               target agent id (required)
  --exclude <id>            do not copy this item (repeatable)
  --conflict <strategy>     ask (default) | keep-target | use-source | compatible | skip
  --apply                   execute the plan (the default output is the plan)
  --yes                     approve non-interactively (requires --apply)
  --dry-run                 print the plan and exit; always accepted
  --project <dir>           project scope rooted at <dir>
  --global                  user scope (the default)
  --help, -h                show this help

Kinds: v1 copies mcp and skill. Any other kind is reported with a reason
(LPSM-COPY-003), never silently dropped. Secrets move as names/references
only: a literal found in the source is reported under Needs and never
written (LPSM-COPY-005). Verification is L1 (read-back fingerprint) and L2
(executable resolvable) only.

Exit codes: 0 ok (including nothing to do) · 1 refused/failed · 2 usage

Examples:
  litespm copy --from opencode --to codex
  litespm copy mcp --from codex --to opencode --conflict keep-target --apply --yes
  litespm copy github --from codex --to opencode --apply
  litespm copy skills --from codex --to opencode --exclude demo --dry-run
`

// copyFlags is the parsed command line of `litespm copy`.
type copyFlags struct {
	from, to    string
	positionals []string
	exclude     []string
	conflict    string
	apply       bool
	yes         bool
	dryRun      bool
	global      bool
	project     string
	showHelp    bool
}

// parseCopyFlags parses the command line strictly: a flag value that is
// missing, an unknown flag, or a strategy that is not one of the five all
// fail (the caller exits 2). `--help` short-circuits so it can exit 0.
func parseCopyFlags(args []string) (copyFlags, error) {
	var f copyFlags
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
		case "--from":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.from = strings.ToLower(strings.TrimSpace(v))
		case "--to":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.to = strings.ToLower(strings.TrimSpace(v))
		case "--exclude":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.exclude = append(f.exclude, v)
		case "--conflict":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.conflict = v
		case "--project":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.project = v
		case "--apply":
			f.apply = true
		case "--yes", "-y":
			f.yes = true
		case "--dry-run":
			f.dryRun = true
		case "--global", "-g":
			f.global = true
		case "--help", "-h":
			f.showHelp = true
			return f, nil
		default:
			if strings.HasPrefix(a, "-") {
				return f, fmt.Errorf("unknown flag %q", a)
			}
			f.positionals = append(f.positionals, a)
		}
	}
	return f, nil
}

// splitPositionals classifies bare arguments: a known kind word (or a
// comma-separated list of them) is a kind selector, anything else is an item
// id. A typo'd kind therefore lands in Items and is reported as NOT FOUND
// rather than silently selecting nothing.
func splitPositionals(tokens []string) (kinds, items []string) {
	for _, tok := range tokens {
		for _, part := range strings.Split(tok, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if kind, ok := porting.CanonicalKind(part); ok {
				kinds = append(kinds, kind)
				continue
			}
			items = append(items, part)
		}
	}
	return kinds, items
}

// runCopy is the dispatch entry point.
func runCopy(args []string) int {
	return runCopyTo(os.Stdout, args)
}

// runCopyTo runs the command against an explicit output writer so tests can
// capture the plan. Prompts also go to out; the TTY gate itself inspects the
// real stdin/stdout, exactly like `approve`/`grant`.
func runCopyTo(out io.Writer, args []string) int {
	flags, err := parseCopyFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: %v\n", err)
		fmt.Fprint(out, copyUsage)
		return 2
	}
	if flags.showHelp {
		fmt.Fprint(out, copyUsage)
		return 0
	}

	// ---- validation: everything that makes this run invalid is decided
	// before a single config is read.
	if flags.from == "" {
		fmt.Fprintln(os.Stderr, "copy: missing --from <host>")
		fmt.Fprint(out, copyUsage)
		return 2
	}
	if flags.to == "" {
		fmt.Fprintln(os.Stderr, "copy: missing --to <host>")
		fmt.Fprint(out, copyUsage)
		return 2
	}
	srcAdapter, err := host.GetAdapter(flags.from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: [LPSM-COPY-001] unknown source agent %q; run 'litespm host list' for the ids\n", flags.from)
		return 2
	}
	tgtAdapter, err := host.GetAdapter(flags.to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: [LPSM-COPY-001] unknown target agent %q; run 'litespm host list' for the ids\n", flags.to)
		return 2
	}
	srcHost := srcAdapter.Descriptor().HostID
	tgtHost := tgtAdapter.Descriptor().HostID
	if srcHost == tgtHost {
		fmt.Fprintf(os.Stderr, "copy: source and target are both %q; there is nothing to port between one agent and itself\n", srcHost)
		return 2
	}
	if _, err := porting.ParseStrategy(flags.conflict); err != nil {
		fmt.Fprintf(os.Stderr, "copy: %v\n", err)
		return 2
	}
	if flags.yes && !flags.apply {
		fmt.Fprintln(os.Stderr, "copy: --yes approves an apply; re-run with --apply --yes (or drop --yes to just print the plan)")
		return 2
	}
	if flags.project != "" && flags.global {
		fmt.Fprintln(os.Stderr, "copy: --project and --global select different scopes; pass only one")
		return 2
	}

	scope := domain.ScopeUser
	projectRoot := ""
	if flags.project != "" {
		abs, err := filepath.Abs(flags.project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "copy: --project %s: %v\n", flags.project, err)
			return 2
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "copy: --project %s: no such directory\n", flags.project)
			return 2
		}
		// Adapters resolve project-scope config paths against the working
		// directory; entering the named project is what makes --project mean
		// one thing for MCP configs and for skill directories alike.
		if err := os.Chdir(abs); err != nil {
			fmt.Fprintf(os.Stderr, "copy: --project %s: %v\n", flags.project, err)
			return 2
		}
		scope = domain.ScopeProject
		projectRoot = abs
	}

	kinds, items := splitPositionals(flags.positionals)
	opts := porting.CopyOptions{
		From:     srcHost,
		To:       tgtHost,
		Kinds:    kinds,
		Items:    items,
		Exclude:  flags.exclude,
		Conflict: flags.conflict,
		Apply:    flags.apply,
		Yes:      flags.yes,
		DryRun:   flags.dryRun,
		Global:   flags.global,
		Project:  flags.project,
	}

	// ---- read + plan. No state database, no writes: this phase must leave
	// every config byte-identical (TestPortingPlanCountsAndNoWrites).
	ctx := context.Background()
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: resolve platform paths: %v\n", err)
		return 1
	}
	src, err := readCopySource(ctx, paths, srcAdapter, scope, projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: read source: %v\n", err)
		return 1
	}
	tgt, err := readCopyTarget(ctx, paths, tgtAdapter, scope, projectRoot, src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: read target: %v\n", err)
		return 1
	}
	plan, err := porting.BuildPlan(src, tgt, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: %v\n", err)
		return 1
	}
	printCopyPlan(out, plan, flags, src, tgt)

	// --dry-run and the default plan mode end here: nothing is written, and
	// the run is a success even when every item was skipped.
	if flags.dryRun || !flags.apply {
		if plan.Found[porting.KindMCP]+plan.Found[porting.KindSkill] > 0 ||
			len(plan.UnsupportedNotes) > 0 || len(plan.NotFound) > 0 {
			fmt.Fprintf(out, "\nApply:  litespm copy --from %s --to %s --apply\n", flags.from, flags.to)
		}
		return 0
	}

	interactive := isTerminal(os.Stdin) && isTerminal(os.Stdout)

	// ---- conflict gate. Only the default `ask` strategy reaches this point
	// with unresolved conflicts; every other strategy was applied while the
	// plan was built and is already visible above.
	if unresolved := plan.Unresolved(); len(unresolved) > 0 {
		decisions := map[string]string{}
		switch {
		case interactive:
			fmt.Fprintf(out, "\n%d conflict(s) need a decision:\n", len(unresolved))
			reader := bufio.NewReader(os.Stdin)
			for _, c := range unresolved {
				decision, aborted := askConflictDecision(reader, out, c)
				if aborted {
					fmt.Fprintln(out, "Aborted; nothing was written.")
					return 1
				}
				decisions[c.Key()] = decision
			}
		case flags.yes:
			// --yes approves the plan AS PRINTED. It is not consent to
			// overwrite: without an explicit strategy the target wins every
			// conflict, and the row says which strategy did it.
			for _, c := range unresolved {
				decisions[c.Key()] = porting.StrategyKeepTarget
			}
			fmt.Fprintf(out, "\nNon-interactive (--yes): keeping the target for %d conflict(s); pass --conflict use-source to overwrite on purpose.\n", len(unresolved))
		default:
			fmt.Fprintf(os.Stderr, "copy: [LPSM-COPY-004] %d unresolved conflict(s) in a non-interactive run; "+
				"re-run with --conflict keep-target|use-source|compatible|skip, or add --yes to approve the plan as printed\n",
				len(unresolved))
			return 1
		}
		if err := plan.ResolveConflicts(decisions); err != nil {
			fmt.Fprintf(os.Stderr, "copy: %v\n", err)
			return 1
		}
		for _, c := range plan.Conflicts {
			if _, decided := decisions[c.Key()]; decided {
				fmt.Fprintf(out, "  decided %s %s → %s\n", c.Kind, c.ID, c.Resolution)
			}
		}
	}

	// ---- approval gate: the plan above is what is being approved.
	writable := plan.Writable()
	if len(writable) == 0 {
		fmt.Fprintln(out, "\nNothing to apply: every item is unchanged, skipped or unsupported.")
		return 0
	}
	if !flags.yes {
		if !interactive {
			fmt.Fprintln(os.Stderr, "copy: --apply needs an interactive terminal to approve, or --yes to approve the printed plan non-interactively")
			return 1
		}
		fmt.Fprintf(out, "\nApply %d change(s) to %s? [y/N] ", len(writable), tgt.Display)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !isAffirmative(line) {
			fmt.Fprintln(out, "Not applied; no changes were made.")
			return 1
		}
	}

	return applyCopyPlan(out, plan, paths, scope, srcHost, tgt)
}

// askConflictDecision prompts for one conflict on an interactive terminal.
// An unrecognised answer re-asks; "a" (or EOF) reports an abort so the run
// stops with nothing written.
func askConflictDecision(in *bufio.Reader, out io.Writer, c porting.Conflict) (string, bool) {
	for {
		fmt.Fprintf(out, "  %s %s (%s): keep target [k] / use source [u] / skip [s] / abort [a]? ",
			c.Kind, c.ID, c.Detail)
		line, err := in.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return "", true // EOF with no answer: treat as abort
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "k", "keep", "keep-target", "y", "yes":
			return porting.StrategyKeepTarget, false
		case "u", "use", "source", "use-source":
			return porting.StrategyUseSource, false
		case "s", "skip":
			return porting.StrategySkip, false
		case "a", "abort", "q", "quit":
			return "", true
		default:
			fmt.Fprintln(out, "  please answer k, u, s or a")
		}
	}
}

// readCopySource performs the read seam of ARCH/16 §6.1: registered entries
// with their env content, plus the adapter's own pre-existing detection, plus
// the skills ledger. It only ever reads.
func readCopySource(ctx context.Context, paths *config.PlatformPaths,
	adapter host.HostAdapter, scope domain.InstallScope, projectRoot string) (*porting.Source, error) {
	d := adapter.Descriptor()
	src := &porting.Source{From: d.HostID, Display: d.DisplayName}

	if keyPath, shape, ok := host.EntryLayout(adapter); ok && keyPath != "" {
		src.EntryShape = shape
		entries, err := host.ListServerEntriesWithValues(ctx, d.HostID, scope)
		if err != nil {
			return nil, fmt.Errorf("list MCP entries of %s: %w", d.HostID, err)
		}
		for _, e := range entries {
			// Endpoint and Transport are the remote half of the IR: a source
			// remote entry must arrive in the plan with its URL, or the copy
			// would write a stdio entry for a server that has no command.
			// Transport may be the host's own discriminator ("http",
			// "streamableHttp", "remote"); NormalizeMCP maps it to the
			// registry vocabulary so both sides of the copy agree.
			src.MCP = append(src.MCP, porting.SourceMCP{
				Name:      e.Name,
				Command:   e.Command,
				Args:      e.Args,
				Endpoint:  e.Endpoint,
				Transport: e.Transport,
				Env:       e.Env,
				EnvNames:  e.EnvNames,
			})
		}
	} else {
		src.Notes = append(src.Notes, fmt.Sprintf(
			"%s declares no MCP entry layout, so no MCP servers were read from it", d.DisplayName))
	}

	// Pre-existing/native components (ARCH/16 §6.1). Merged by name so a
	// component the entry reader missed — a legacy layout, an entry without a
	// command — is still planned (and reported) rather than lost. Detection
	// has no project scope in the adapter contract, so it is consulted for
	// user scope only; a project-scope copy reads exactly the project file it
	// will write.
	if scope == domain.ScopeUser {
		comps, err := adapter.DetectPreExistingComponents(ctx)
		if err != nil {
			src.Notes = append(src.Notes, fmt.Sprintf(
				"pre-existing component detection failed on %s: %v (registered entries were still read)", d.DisplayName, err))
		}
		known := map[string]bool{}
		for _, m := range src.MCP {
			known[m.Name] = true
		}
		for _, c := range comps {
			if known[c.Name] {
				continue
			}
			known[c.Name] = true
			switch strings.ToLower(strings.TrimSpace(c.Kind)) {
			case "mcp", "":
				src.MCP = append(src.MCP, porting.SourceMCP{
					Name: c.Name, Command: c.Command, Args: c.Args,
				})
			case "skill":
				// A skill component carries no directory, so there is nothing
				// to port; say so instead of dropping it silently.
				src.Notes = append(src.Notes, fmt.Sprintf(
					"skill %q is visible in %s config but has no skills-ledger directory; install it with 'litespm skills add' to make it portable",
					c.Name, d.DisplayName))
			default:
				src.Other = append(src.Other, porting.SourceOther{Kind: c.Kind, Name: c.Name})
			}
		}
	}

	// Skills: the ledger records what this tool wrote where, and the entry's
	// directory is the source tree a copy reads. Skills the user placed by
	// hand (never ledgered) are not ported — the ledger is the only record
	// that says which agent a directory belongs to.
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	ledgerScope := copyLedgerScope(scope)
	skillDir, hasDir := skills.AgentSkillDir(d.HostID, ledgerScope, projectRoot, home)
	if !hasDir {
		src.Notes = append(src.Notes, fmt.Sprintf(
			"%s has no documented skills directory, so no skills were read from it", d.DisplayName))
	} else {
		ledger, lerr := skills.OpenLedger(skills.LedgerPath(paths.DataRoot))
		if lerr != nil {
			return nil, fmt.Errorf("open skills ledger: %w", lerr)
		}
		entries, lerr := ledger.All()
		if lerr != nil {
			return nil, fmt.Errorf("read skills ledger: %w", lerr)
		}
		seen := map[string]bool{}
		for _, e := range entries {
			if e.Scope != ledgerScope || filepath.Dir(e.DestDir) != skillDir || seen[e.SkillName] {
				continue
			}
			seen[e.SkillName] = true
			// The provenance digest is captured from the files that would
			// actually be written; see porting.SourceSkill.
			prov, perr := skills.CaptureProvenance(e.DestDir)
			if perr != nil {
				src.Skills = append(src.Skills, porting.SourceSkill{Name: e.SkillName})
				continue
			}
			src.Skills = append(src.Skills, porting.SourceSkill{
				Name:          e.SkillName,
				SourceDir:     e.DestDir,
				ContentDigest: prov.Digest,
			})
		}
	}
	return src, nil
}

// readCopyTarget performs the support check and the conflict read: what the
// target can represent, and what it already has.
func readCopyTarget(ctx context.Context, paths *config.PlatformPaths,
	adapter host.HostAdapter, scope domain.InstallScope, projectRoot string, src *porting.Source) (*porting.TargetCaps, error) {
	d := adapter.Descriptor()
	tgt := &porting.TargetCaps{
		To:             d.HostID,
		Display:        d.DisplayName,
		ExistingMCP:    map[string]host.HostServerEntry{},
		ExistingSkills: map[string]porting.TargetSkill{},
	}
	// The target's remote (URL) capability comes from the one capability
	// query install also uses, so the plan can never promise a write install
	// would refuse. nil is the fail-closed answer: a remote source row against
	// this target is reported "not copyable" with its reason, never rendered
	// as a stdio entry.
	if remoteSpec, ok := host.RemoteEntrySpecFor(d.HostID); ok {
		tgt.Remote = &remoteSpec
	}
	if keyPath, shape, ok := host.EntryLayout(adapter); ok && keyPath != "" {
		tgt.HasEntrySpec = true
		tgt.EntryShape = shape
		entries, err := host.ListServerEntriesWithValues(ctx, d.HostID, scope)
		if err != nil {
			return nil, fmt.Errorf("list MCP entries of %s: %w", d.HostID, err)
		}
		for _, e := range entries {
			tgt.ExistingMCP[e.Name] = e
		}
		// Detection covers what the entry reader skipped — an entry with no
		// command (the reader drops those) or a legacy container layout. A
		// name that exists on the target must surface as a plan conflict, not
		// as a surprise name-collision error halfway through apply. Those
		// overlay rows carry no env content (detection reads none), which is
		// conservative: a mismatch classifies as a conflict for the user to
		// decide, never as a silent overwrite.
		if scope == domain.ScopeUser {
			comps, _ := adapter.DetectPreExistingComponents(ctx)
			for _, c := range comps {
				kind := strings.ToLower(strings.TrimSpace(c.Kind))
				if kind != "mcp" && kind != "" {
					continue
				}
				if _, ok := tgt.ExistingMCP[c.Name]; ok {
					continue
				}
				tgt.ExistingMCP[c.Name] = host.HostServerEntry{
					Name: c.Name, Command: c.Command, Args: c.Args,
				}
			}
		}
	}
	_, tgt.CanForwardEnv = host.EnvForwarding(d.HostID)

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	ledgerScope := copyLedgerScope(scope)
	skillDir, hasDir := skills.AgentSkillDir(d.HostID, ledgerScope, projectRoot, home)
	if hasDir {
		tgt.SkillDir = skillDir
		ledger, lerr := skills.OpenLedger(skills.LedgerPath(paths.DataRoot))
		if lerr != nil {
			return nil, fmt.Errorf("open skills ledger: %w", lerr)
		}
		entries, lerr := ledger.All()
		if lerr != nil {
			return nil, fmt.Errorf("read skills ledger: %w", lerr)
		}
		managed := map[string]bool{}
		for _, e := range entries {
			managed[e.DestDir] = true
		}
		for _, s := range src.Skills {
			if s.Name == "" {
				continue
			}
			target := filepath.Join(skillDir, s.Name)
			if _, serr := os.Lstat(target); serr != nil {
				continue // absent: not a conflict
			}
			ts := porting.TargetSkill{Dir: target, Managed: managed[target]}
			if prov, perr := skills.CaptureProvenance(target); perr == nil {
				ts.Digest = prov.Digest
			}
			tgt.ExistingSkills[s.Name] = ts
		}
	}
	return tgt, nil
}

// copyLedgerScope maps the install scope onto the skills ledger's own
// vocabulary ("global" for user scope).
func copyLedgerScope(scope domain.InstallScope) string {
	if scope == domain.ScopeProject {
		return "project"
	}
	return "global"
}

// ---- plan output ------------------------------------------------------------

// copyKindNoun renders a kind for a count line: "MCP server(s)" in the Found
// line, the short "MCP" in action lines (ARCH/38 §5.4).
func copyKindNoun(kind string, n int, full bool) string {
	switch kind {
	case porting.KindMCP:
		if full {
			if n == 1 {
				return "MCP server"
			}
			return "MCP servers"
		}
		return "MCP"
	default:
		if n == 1 {
			return kind
		}
		return kind + "s"
	}
}

// copyCountOrder fixes the display order of kinds: mcp, skill, plugin first,
// everything else alphabetical after them.
func copyCountOrder(counts map[string]int) []string {
	var order []string
	seen := map[string]bool{}
	for _, kind := range []string{porting.KindMCP, porting.KindSkill, porting.KindPlugin} {
		if counts[kind] > 0 {
			order = append(order, kind)
			seen[kind] = true
		}
	}
	var rest []string
	for kind, n := range counts {
		if n > 0 && !seen[kind] {
			rest = append(rest, kind)
		}
	}
	// stable, honest ordering for kinds outside the first three
	for i := 1; i < len(rest); i++ {
		for j := i; j > 0 && rest[j] < rest[j-1]; j-- {
			rest[j], rest[j-1] = rest[j-1], rest[j]
		}
	}
	return append(order, rest...)
}

// formatCopyCounts renders "14 MCP servers · 8 skills · 3 plugins" from a
// per-kind count map (empty string when nothing was found).
func formatCopyCounts(counts map[string]int, full bool) string {
	var parts []string
	for _, kind := range copyCountOrder(counts) {
		parts = append(parts, fmt.Sprintf("%d %s", counts[kind], copyKindNoun(kind, counts[kind], full)))
	}
	return strings.Join(parts, " · ")
}

// skipLabel groups a skipped row for the SKIPPED count line.
func skipLabel(reason string) string {
	switch {
	case reason == "already identical on the target":
		return "already identical"
	case reason == "excluded by --exclude":
		return "excluded"
	case strings.HasPrefix(reason, "conflict:"):
		return "conflict, target kept"
	default:
		return "skipped"
	}
}

// printCopyPlan renders the plan: counts by action, rows, needs, then the
// apply hint. It prints names and fingerprints only — never an env value.
func printCopyPlan(out io.Writer, plan *porting.CopyPlan, f copyFlags, src *porting.Source, tgt *porting.TargetCaps) {
	mode := "   (no changes have been made)"
	if f.apply && !f.dryRun {
		mode = ""
	}
	fmt.Fprintf(out, "Copy %s → %s%s\n\n", src.Display, tgt.Display, mode)

	total := 0
	for _, n := range plan.Found {
		total += n
	}
	if total == 0 {
		fmt.Fprintf(out, "Found     nothing on %s: no MCP servers, skills or other components were read\n", src.Display)
	} else {
		fmt.Fprintf(out, "Found     %s\n", formatCopyCounts(plan.Found, true))
	}
	if line := formatCopyCounts(plan.CountKind(porting.ActionDirect), false); line != "" {
		fmt.Fprintf(out, "DIRECT    %s\n", line)
	}
	if line := formatCopyCounts(plan.CountKind(porting.ActionTranslated), false); line != "" {
		fmt.Fprintf(out, "TRANSLATED %s\n", line)
	}

	// SKIPPED: grouped by why, because "skipped" alone hides whether the
	// item was already there or deliberately left alone.
	skips := map[string]int{}
	skipKinds := map[string]string{}
	for _, it := range plan.Items {
		if it.Action != porting.ActionSkipped {
			continue
		}
		label := skipLabel(it.Reason)
		skips[label]++
		skipKinds[label] = it.Kind
	}
	if len(skips) > 0 {
		var parts []string
		for _, label := range []string{"already identical", "excluded", "conflict, target kept", "skipped"} {
			if skips[label] == 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%d %s %s", skips[label],
				copyKindNoun(skipKinds[label], skips[label], false), label))
		}
		fmt.Fprintf(out, "SKIPPED   %s\n", strings.Join(parts, " · "))
	}
	if line := formatCopyCounts(plan.CountKind(porting.ActionUnsupported), false); line != "" {
		fmt.Fprintf(out, "UNSUPPORTED %s (LPSM-COPY-003; reasons in the rows below)\n", line)
	}
	for _, c := range plan.Conflicts {
		state := "unresolved"
		if c.Resolution != "" {
			state = c.Resolution
		}
		fmt.Fprintf(out, "CONFLICTS %s %s — %s [%s]\n", c.Kind, c.ID, c.Detail, state)
	}
	if needs := plan.AllNeeds(); len(needs) > 0 {
		fmt.Fprintf(out, "SECRETS   %s — name only; value NOT copied (LPSM-COPY-005)\n", strings.Join(needs, ", "))
	}
	for _, id := range plan.NotFound {
		fmt.Fprintf(out, "NOT FOUND %s — not present on %s\n", id, src.Display)
	}
	for _, n := range plan.UnsupportedNotes {
		fmt.Fprintf(out, "UNSUPPORTED %s\n", n)
	}
	for _, n := range src.Notes {
		fmt.Fprintf(out, "NOTE      %s\n", n)
	}

	if len(plan.Items) > 0 {
		fmt.Fprintln(out, "\nRows:")
		for _, it := range plan.Items {
			detail := it.TargetDiff
			if it.Reason != "" {
				if detail != "" {
					detail += " — "
				}
				detail += it.Reason
			}
			if detail == "" {
				detail = "will be written through the normal install path"
			}
			fmt.Fprintf(out, "  %-7s %-24s %-12s %s\n", it.Kind, it.ID, it.Action, detail)
		}
	}

	verify := map[string]bool{}
	writable := plan.Writable()
	for _, it := range writable {
		for _, v := range it.Verify {
			verify[v] = true
		}
	}
	if len(verify) > 0 {
		var steps []string
		for _, s := range []string{porting.VerifyL1Config, porting.VerifyL2Exe, porting.VerifyL1SkillDir} {
			if verify[s] {
				steps = append(steps, s)
			}
		}
		fmt.Fprintf(out, "\nVerify    %s (levels 1–2 only)\n", strings.Join(steps, " · "))
	}
}

// ---- apply ------------------------------------------------------------------

// copyItemResult is the per-item outcome printed after a write.
type copyItemResult struct {
	Item      porting.CopyItem
	InstallID string
	Path      string
	L1        string
	L2        string
	Err       error
}

func printCopyResult(out io.Writer, r copyItemResult) {
	if r.Err != nil {
		fmt.Fprintf(out, "  ✗ %-6s %-24s %v\n", r.Item.Kind, r.Item.ID, r.Err)
		return
	}
	detail := "L1 " + r.L1
	if r.L2 != "" {
		detail += " · L2 " + r.L2
	}
	fmt.Fprintf(out, "  ✓ %-6s %-24s install %s → %s (%s)\n",
		r.Item.Kind, r.Item.ID, r.InstallID, r.Path, detail)
}

// applyCopyPlan executes the approved plan. Every item is its own managed
// unit: its own install transaction, its own compensation, its own
// verification — one item failing does not strand the others, and every
// failure is reported and reflected in the exit code.
func applyCopyPlan(out io.Writer, plan *porting.CopyPlan, paths *config.PlatformPaths,
	scope domain.InstallScope, srcHost string, tgt *porting.TargetCaps) int {
	ctx := context.Background()
	if err := paths.EnsureDirectories(); err != nil {
		fmt.Fprintf(os.Stderr, "copy: initialize storage directories: %v\n", err)
		return 1
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "copy: open state database: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	overwrites := plan.Overwrites()
	writable := plan.Writable()
	fmt.Fprintf(out, "\nApplying %d change(s) to %s...\n", len(writable), tgt.Display)

	var (
		skillLedger    *skills.Ledger
		skillInstaller *skills.Installer
		applied        int
		failed         int
	)
	for _, item := range writable {
		force := overwrites[porting.ConflictKey(item.Kind, item.ID)]
		var res copyItemResult
		var err error
		switch item.Kind {
		case porting.KindMCP:
			res, err = applyMCPItem(ctx, db, paths, srcHost, tgt.To, scope, item, force)
		case porting.KindSkill:
			if skillInstaller == nil {
				skillLedger, skillInstaller, err = newCopySkillWriter(db, paths.DataRoot)
				if err != nil {
					fmt.Fprintf(os.Stderr, "copy: %v\n", err)
					return 1
				}
			}
			if err == nil {
				res, err = applySkillItem(ctx, db, paths, skillLedger, skillInstaller,
					srcHost, tgt.To, scope, item, force, tgt.SkillDir)
			}
		default:
			err = fmt.Errorf("LPSM-COPY-003: kind %q is not portable in v1", item.Kind)
		}
		res.Item = item
		if err != nil {
			failed++
			if res.Err == nil {
				res.Err = err
			}
			printCopyResult(out, res)
			continue
		}
		applied++
		printCopyResult(out, res)
	}

	skipped := len(plan.Items) - len(writable)
	fmt.Fprintf(out, "\nApplied %d · failed %d · skipped %d\n", applied, failed, skipped)
	if applied > 0 {
		fmt.Fprintln(out, "Reverse a copied install with `litespm install remove <installId>` (or `litespm restore <installId>`).")
	}
	if failed > 0 {
		fmt.Fprintln(os.Stderr, "copy: one or more items failed; each failed item's own install transaction was rolled back (LPSM-COPY-007 when verification was the cause)")
		return 1
	}
	return 0
}

// applyMCPItem writes one MCP entry through the same path `install` uses —
// backup, registration, deployment row, install transaction — then verifies
// it by reading it back before the transaction commits.
func applyMCPItem(ctx context.Context, db *state.DB, paths *config.PlatformPaths,
	srcHost, hostID string, scope domain.InstallScope, item porting.CopyItem, force bool) (copyItemResult, error) {
	res := copyItemResult{Item: item}
	txn := lifecycle.BeginInstall(ctx)
	defer txn.RollbackUnlessCommitted()

	written, err := host.InstallServerEntry(ctx, hostID, item.IR, host.EntryInstallOptions{
		BackupDir: paths.BackupsPath(),
		Scope:     scope,
		Force:     force,
	})
	if err != nil {
		res.Err = err
		return res, fmt.Errorf("write the entry: %w", err)
	}
	txn.Undo(func(ctx context.Context) error { return undoHostConfigWrite(written) })
	res.Path = written.ConfigPath

	// L1 (fatal): re-read the entry from the target config and compare its
	// fingerprint with the plan's IR. A mismatch means the write did not
	// produce what was approved — the transaction rolls back and the item
	// reports LPSM-COPY-007.
	l1Detail, l1err := verifyMCPReadBack(ctx, hostID, scope, item)
	res.L1 = l1Detail
	if l1err != nil {
		cause := fmt.Errorf("LPSM-COPY-007: verification failed after write for %s %s: %s", item.Kind, item.ID, l1Detail)
		return res, errors.Join(cause, txn.Rollback())
	}
	// L2 is advisory: a copied server the machine cannot run yet is still a
	// correct config, and saying so beats failing the copy (LPSM-COPY-006).
	res.L2 = verifyExecutable(item.IR.Command)

	listingID := porting.AdoptedListingID(item.Kind, srcHost, item.ID)
	// The tree digest is the IR's own fingerprint: the identity of what was
	// registered, in the same "sha256:..." shape every other install records.
	entryDigest := porting.Fingerprint(item.IR)
	installID := fmt.Sprintf("inst_%s_%s_%s", scope, safeInstallIDPart(listingID), shortDigest(entryDigest))
	now := time.Now().UTC()
	rec := &domain.InstallRecord{
		InstallID:   installID,
		ListingID:   listingID,
		Kind:        domain.KindMCP,
		Version:     "", // not knowable: this entry never came from a catalog
		TreeDigest:  entryDigest,
		InstallPath: written.ConfigPath,
		Scope:       scope,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	// Install row, component row, host registration and the deployment
	// mutation in ONE transaction, exactly as an install records them — that
	// is what makes `install remove` and `restore` reverse this copy.
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := db.SaveInstallExec(ctx, tx, rec); err != nil {
			return fmt.Errorf("record the install: %w", err)
		}
		if err := db.SaveInstallComponentExec(ctx, tx, &domain.InstallComponentRecord{
			InstallID:     installID,
			Kind:          domain.ComponentMCPProvider,
			ComponentName: item.ID,
			Path:          written.ConfigPath,
		}); err != nil {
			return fmt.Errorf("record the installed component: %w", err)
		}
		if err := db.SaveHostRegistrationExec(ctx, tx, &domain.HostRegistrationRecord{
			HostID:           written.HostID,
			Scope:            scope,
			ConfigPath:       written.ConfigPath,
			ConfigFormat:     string(hostConfigFormatFor(written.HostID)),
			ManagedEntryKey:  item.ID,
			EntryFingerprint: written.Fingerprint,
			RegisteredAt:     now,
			Status:           "active",
		}); err != nil {
			return fmt.Errorf("record the host registration: %w", err)
		}
		m := lifecycle.OwnedWrite(installID, listingID, written.HostID, string(scope),
			written.ConfigPath, hostStructureTypeFor(written.HostID),
			hostLocatorFor(written.HostID, item.ID),
			written.PriorFingerprint, written.Fingerprint, written.PriorEntry, written.BackupPath)
		if err := deployment.SaveMutationExec(ctx, tx, m); err != nil {
			return fmt.Errorf("record the deployment mutation: %w", err)
		}
		return nil
	}); err != nil {
		res.Err = err
		return res, errors.Join(fmt.Errorf("record the copied install: %w", err), txn.Rollback())
	}
	if err := txn.Commit(); err != nil {
		res.Err = err
		return res, err
	}
	res.InstallID = installID
	return res, nil
}

// verifyMCPReadBack is verification level 1: the target re-parses and the
// entry's fingerprint equals the plan's rendered entry.
func verifyMCPReadBack(ctx context.Context, hostID string, scope domain.InstallScope, item porting.CopyItem) (string, error) {
	entries, err := host.ListServerEntriesWithValues(ctx, hostID, scope)
	if err != nil {
		return fmt.Sprintf("failed: could not re-read the target config: %v", err), err
	}
	for _, e := range entries {
		if e.Name != item.ID {
			continue
		}
		got := porting.Fingerprint(porting.IRFromHostEntry(e))
		want := porting.Fingerprint(item.IR)
		if got != want {
			return fmt.Sprintf("failed: read-back fingerprint %s != plan %s", got, want),
				fmt.Errorf("entry fingerprint mismatch")
		}
		return "ok", nil
	}
	return "failed: the entry is not readable from the target config", fmt.Errorf("entry missing after write")
}

// verifyExecutable is verification level 2: does Command resolve on this
// machine? Advisory — reported, never a reason to refuse the copy.
func verifyExecutable(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "not resolvable: the entry has no command (LPSM-COPY-006)"
	}
	if strings.ContainsAny(command, `/\`) {
		if fi, err := os.Stat(command); err == nil && !fi.IsDir() {
			return "ok"
		}
		return fmt.Sprintf("not resolvable: %s (LPSM-COPY-006)", command)
	}
	if _, err := exec.LookPath(command); err == nil {
		return "ok"
	}
	return fmt.Sprintf("not resolvable: %q is not on PATH (LPSM-COPY-006)", command)
}

// newCopySkillWriter builds the skills ledger + policy-gated installer a
// skill copy writes through. The policy engine runs with its real tiers, and
// its "ask" is answered only by the approval this run already obtained (the
// plan is printed and approved before applyCopyPlan is ever called).
func newCopySkillWriter(db *state.DB, dataRoot string) (*skills.Ledger, *skills.Installer, error) {
	ledger, err := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if err != nil {
		return nil, nil, fmt.Errorf("open skills ledger: %w", err)
	}
	cfg, _ := config.LoadCurrentConfig()
	engine, perr := newPolicyEngine(db, dataRoot, policyDefaultsFrom(cfg))
	if perr != nil {
		return nil, nil, fmt.Errorf("load policy: %w", perr)
	}
	checker := enginePolicyChecker(engine, func(_ context.Context, _ skills.PolicyRequest, _ string) bool {
		// Reached only inside applyCopyPlan, i.e. only after the printed plan
		// was approved (a terminal "yes" or --yes). That approval answers the
		// ask; a deny from the engine is never overridden here.
		return true
	})
	return ledger, skills.NewInstaller(ledger, checker), nil
}

// applySkillItem copies one skill into the target agent's skill directory
// through the normal skills installer (policy consult, safe copy, ledger
// entry), then verifies by content digest before committing state.
func applySkillItem(ctx context.Context, db *state.DB, paths *config.PlatformPaths,
	ledger *skills.Ledger, installer *skills.Installer,
	srcHost, hostID string, scope domain.InstallScope, item porting.CopyItem,
	force bool, targetSkillDir string) (copyItemResult, error) {
	res := copyItemResult{Item: item}
	if targetSkillDir == "" {
		err := fmt.Errorf("LPSM-COPY-002: %s has no skills directory to write into", hostID)
		res.Err = err
		return res, err
	}
	dest := filepath.Join(targetSkillDir, item.Skill.Name)
	ledgerScope := copyLedgerScope(scope)
	txn := lifecycle.BeginInstall(ctx)
	defer txn.RollbackUnlessCommitted()

	var entry skills.LedgerEntry
	var prior skills.LedgerEntry // set on a use-source overwrite
	if force {
		// use-source on a skill: replace the existing managed copy through
		// the installer's update path (inventory guard, atomic swap). The
		// planner already refused an unmanaged directory, so the ledger row
		// must exist — and if it does not, refuse rather than delete.
		found, old, err := findLedgerEntry(ledger, dest)
		if err != nil {
			res.Err = err
			return res, err
		}
		if !found {
			err := fmt.Errorf("use-source refused: %s is not in the skills ledger; copy never deletes an unmanaged tree", dest)
			res.Err = err
			return res, err
		}
		prior = old
		// Byte-exact backup taken BEFORE the swap, so a failure later in
		// this unit restores exactly what was there — Update deletes its own
		// backup as soon as the ledger is updated.
		backupDir := filepath.Join(paths.BackupsPath(),
			fmt.Sprintf("skill-%s-%d", safeInstallIDPart(item.Skill.Name), time.Now().UnixNano()))
		if err := skills.CopySkillDir(dest, backupDir); err != nil {
			err = fmt.Errorf("back up the existing skill before overwrite: %w", err)
			res.Err = err
			return res, err
		}
		outcome, uerr := installer.Update(ctx, skills.UpdateRequest{
			Entry:  old,
			NewDir: item.Skill.SourceDir,
			Force:  true,
		})
		if uerr != nil {
			_ = os.RemoveAll(backupDir)
			res.Err = uerr
			return res, fmt.Errorf("replace the skill at %s: %w", dest, uerr)
		}
		entry = outcome.Entry
		txn.Undo(func(ctx context.Context) error {
			return restoreSkillOverwrite(ledger, dest, prior, backupDir)
		})
	} else {
		op := skills.InstallOp{
			SkillName: item.Skill.Name,
			FromDir:   item.Skill.SourceDir,
			ToDir:     dest,
			HostLabel: hostID,
		}
		var err error
		entry, err = installer.Install(ctx, skills.InstallRequest{
			Op:     op,
			Source: skills.SkillSource{Kind: "local", Display: item.Skill.SourceDir, LocalDir: item.Skill.SourceDir},
			Scope:  ledgerScope,
		})
		if err != nil {
			res.Err = err
			return res, fmt.Errorf("install the skill: %w", err)
		}
		txn.Undo(func(ctx context.Context) error { return undoSkillInstall(ledger, entry) })
	}
	res.Path = dest

	// L1 (fatal): the content on disk digests to exactly what the plan
	// approved. A mismatch rolls the whole unit back.
	prov, perr := skills.CaptureProvenance(dest)
	if perr != nil {
		res.L1 = fmt.Sprintf("failed: could not fingerprint the written skill: %v", perr)
		cause := fmt.Errorf("LPSM-COPY-007: verification failed after write for %s %s", item.Kind, item.ID)
		return res, errors.Join(cause, txn.Rollback())
	}
	if prov.Digest != item.Skill.ContentDigest {
		res.L1 = fmt.Sprintf("failed: digest %s != plan %s", prov.Digest, item.Skill.ContentDigest)
		cause := fmt.Errorf("LPSM-COPY-007: verification failed after write for %s %s", item.Kind, item.ID)
		return res, errors.Join(cause, txn.Rollback())
	}
	res.L1 = "ok"

	listingID := porting.AdoptedListingID(item.Kind, srcHost, item.ID)
	installID := fmt.Sprintf("inst_%s_%s_%s", scope, safeInstallIDPart(listingID), shortDigest(entry.ContentDigest))
	now := time.Now().UTC()
	preImage := "" // a fresh install refuses an existing destination, so there was nothing there
	if force {
		preImage = prior.ContentDigest // the digest this overwrite replaced
	}
	rec := &domain.InstallRecord{
		InstallID:   installID,
		ListingID:   listingID,
		Kind:        domain.KindSkill,
		Version:     "", // not knowable: this skill never came from a catalog
		TreeDigest:  entry.ContentDigest,
		InstallPath: dest,
		Scope:       scope,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := db.SaveInstallExec(ctx, tx, rec); err != nil {
			return fmt.Errorf("record the install: %w", err)
		}
		if err := db.SaveInstallComponentExec(ctx, tx, &domain.InstallComponentRecord{
			InstallID:     installID,
			Kind:          domain.ComponentSkill,
			ComponentName: item.Skill.Name,
			Path:          dest,
		}); err != nil {
			return fmt.Errorf("record the installed component: %w", err)
		}
		m := lifecycle.OwnedWrite(installID, listingID, "skills-ledger", string(scope),
			dest, "skill-dir", "skills."+item.Skill.Name,
			preImage, entry.ContentDigest, "", "")
		if err := deployment.SaveMutationExec(ctx, tx, m); err != nil {
			return fmt.Errorf("record the deployment mutation: %w", err)
		}
		return nil
	}); err != nil {
		res.Err = err
		return res, errors.Join(fmt.Errorf("record the copied install: %w", err), txn.Rollback())
	}
	if err := txn.Commit(); err != nil {
		res.Err = err
		return res, err
	}
	res.InstallID = installID
	return res, nil
}

// findLedgerEntry looks up the skills-ledger row that owns dest.
func findLedgerEntry(ledger *skills.Ledger, dest string) (bool, skills.LedgerEntry, error) {
	entries, err := ledger.All()
	if err != nil {
		return false, skills.LedgerEntry{}, fmt.Errorf("read skills ledger: %w", err)
	}
	for _, e := range entries {
		if e.DestDir == dest {
			return true, e, nil
		}
	}
	return false, skills.LedgerEntry{}, nil
}

// restoreSkillOverwrite is the compensation for a use-source skill replace:
// the copied tree goes, the byte-exact backup returns, and the ledger row
// reverts to what it recorded before the swap. The ledger must never
// describe files that are gone, so it is restored after the directory.
func restoreSkillOverwrite(ledger *skills.Ledger, dest string, prior skills.LedgerEntry, backup string) error {
	if err := os.RemoveAll(dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing copied skill %s: %w", dest, err)
	}
	if err := os.Rename(backup, dest); err != nil {
		return fmt.Errorf("restoring skill backup %s: %w", backup, err)
	}
	if err := ledger.ReplaceEntry(dest, prior); err != nil {
		return fmt.Errorf("restore the ledger row for %s: %w", dest, err)
	}
	return nil
}
