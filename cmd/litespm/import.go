package main

// import.go — `litespm import`: bring a foreign package-manager format into
// LiteSPM (ARCH/32 §5). Import-only for v1.
//
//	read → validate (bounded, strict) → normalize (internal/interop)
//	→ PLAN PREVIEW printed unconditionally (nothing written)
//	→ approval (interactive terminal y/N, or --apply --yes)
//	→ write the project manifest only.
//
// What this command never does, by construction:
//
//   - no install: installing is the separate `litespm install` step;
//   - no lock write: a litespm.lock entry must be reproducible from the
//     manifest plus the local catalog (ARCH/32 invariants), so foreign
//     resolved data stops at the plan record and `litespm lock` runs later;
//   - no network, and nothing the input file names is ever executed —
//     `command` marketplace sources are refused (LPSM-IMPORT-005).
//
// The plan prints before every decision, with the Skipped/lossy section
// that states what the manifest cannot carry. Exit codes follow ARCH/20:
// 0 ok (including nothing to do), 1 refused/failed, 2 usage. The named
// codes LPSM-IMPORT-001..007 live in internal/interop.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sarv-projects/litespm/internal/interop"
	"github.com/sarv-projects/litespm/internal/manifest"
)

const importUsage = `Usage: litespm import <format> <path> [flags]

Bring a foreign package-manager file into this project's manifest
(litespm.toml / litespm.yml). The plan always prints before anything is
written; nothing is written until you approve it.

Formats:
  skills-lock           Vercel skills-lock.json (project v1 / global v3)
  server.json           MCP registry server.json (object or array)
  claude-marketplace    .claude-plugin/marketplace.json
  codex-marketplace     .agents/plugins/marketplace.json
  plugin                Agent Plugins 1.0.0 plugin.json

Flags:
  --project <dir>    project scope rooted at <dir> (default: this directory)
  --json             machine-readable plan on stdout
  --apply            write the approved plan (the default output is the plan)
  --yes, -y          approve non-interactively (requires --apply)
  --dry-run          print the plan and exit; always accepted
  --help, -h         show this help

Untrusted input: files are capped at 8 MiB, nesting at 64 containers, ids
and paths containing ".." or absolute are refused (LPSM-IMPORT-003), and
'command' sources are rejected (LPSM-IMPORT-005) — import never executes
or fetches anything the file names.

Import writes the manifest only: no install (that is 'litespm install'),
no litespm.lock (that is 'litespm lock'), no network.
Exit codes: 0 ok (including nothing to do) · 1 refused/failed · 2 usage
`

// importFlags is the parsed command line of `litespm import`.
type importFlags struct {
	format   string
	path     string
	project  string
	jsonOut  bool
	apply    bool
	yes      bool
	dryRun   bool
	showHelp bool
}

// parseImportFlags parses the command line strictly: exactly two
// positionals (<format> <path>), known flags only, each flag that needs a
// value having one. `--help` short-circuits so it can exit 0.
func parseImportFlags(args []string) (importFlags, error) {
	var f importFlags
	var positionals []string
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
		case "--help", "-h":
			f.showHelp = true
			return f, nil
		case "--project":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.project = v
		case "--json":
			f.jsonOut = true
		case "--apply":
			f.apply = true
		case "--yes", "-y":
			f.yes = true
		case "--dry-run":
			f.dryRun = true
		default:
			if strings.HasPrefix(a, "-") {
				return f, fmt.Errorf("unknown flag %q", a)
			}
			positionals = append(positionals, a)
		}
	}
	if len(positionals) != 2 {
		return f, fmt.Errorf("expected <format> <path>, got %d argument(s)", len(positionals))
	}
	f.format = positionals[0]
	f.path = positionals[1]
	return f, nil
}

// runImport is the dispatch entry point.
func runImport(args []string) int {
	return runImportTo(os.Stdout, args)
}

// runImportTo runs the command against an explicit output writer so tests
// can capture the plan. The approval prompt also goes to out; the TTY gate
// itself inspects the real stdin/stdout, exactly like `copy`/`approve`.
func runImportTo(out io.Writer, args []string) int {
	flags, err := parseImportFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "import: %v\n", err)
		fmt.Fprint(out, importUsage)
		return 2
	}
	if flags.showHelp {
		fmt.Fprint(out, importUsage)
		return 0
	}
	if flags.yes && !flags.apply {
		fmt.Fprintln(os.Stderr, "import: --yes approves an apply; re-run with --apply --yes (or drop --yes to just print the plan)")
		return 2
	}
	format, err := interop.NormalizeFormat(flags.format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "import: %v\n", err)
		fmt.Fprint(out, importUsage)
		return 2
	}

	// ---- project scope. `--project` names the directory the manifest
	// lives in (or is created in); the default is the working directory.
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "import: resolve working directory: %v\n", err)
		return 1
	}
	if flags.project != "" {
		abs, aerr := filepath.Abs(flags.project)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "import: --project %s: %v\n", flags.project, aerr)
			return 2
		}
		info, serr := os.Stat(abs)
		if serr != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "import: --project %s: no such directory\n", flags.project)
			return 2
		}
		dir = abs
	}

	// ---- read + parse. This phase must write nothing: it opens exactly
	// the one file the user named, bounded and validated as untrusted input.
	inputPath := flags.path
	if info, serr := os.Stat(inputPath); serr != nil {
		fmt.Fprintf(os.Stderr, "import: input %s: %v\n", inputPath, serr)
		return 1
	} else if info.IsDir() {
		inputPath = filepath.Join(inputPath, interop.DefaultFileName(format))
	}
	data, rerr := interop.ReadFileBounded(inputPath)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "import: %v\n", rerr)
		return 1
	}
	doc, perr := interop.Parse(format, data)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "import: %v\n", perr)
		return 1
	}
	doc.SourcePath = inputPath

	// ---- target manifest: the project one if it exists (walking up, the
	// same rule `lock` uses), otherwise a new litespm.toml in dir.
	targetPath, exists, terr := resolveImportTarget(dir)
	if terr != nil {
		fmt.Fprintf(os.Stderr, "import: %v\n", terr)
		return 1
	}
	var existing *manifest.Manifest
	if exists {
		m, merr := manifest.Load(targetPath)
		if merr != nil {
			fmt.Fprintf(os.Stderr, "import: cannot merge into %s: %v\n", targetPath, merr)
			return 1
		}
		existing = m
	}

	plan := interop.BuildPlan(doc, targetPath, existing)

	// ---- the plan prints unconditionally, before any decision.
	interactive := !flags.jsonOut && isTerminal(os.Stdin) && isTerminal(os.Stdout)
	if flags.jsonOut {
		// JSON is emitted once, at the outcome: approved runs report the
		// write, everything else reports the preview. Refusals print the
		// preview first (applied=false) and fail on stderr.
		return importJSONOutcome(out, plan, flags, interactive)
	}

	plan.Render(out)
	approval := interop.Approval{
		Apply:       flags.apply,
		Yes:         flags.yes,
		JSONOut:     flags.jsonOut,
		DryRun:      flags.dryRun,
		Interactive: interactive,
	}
	decision, msg := approval.Decide()
	switch decision {
	case interop.DecisionRefused:
		fmt.Fprintf(os.Stderr, "import: %s\n", msg)
		return 1
	case interop.DecisionPreview:
		return 0
	}
	// Approved (or about to prompt): nothing to write is a success with a
	// plain sentence, not a pointless y/N about zero changes.
	if plan.Actionable() == 0 {
		fmt.Fprintln(out, "\nNothing to apply: every imported entry is already present in the manifest.")
		return 0
	}
	if decision == interop.DecisionPrompt {
		fmt.Fprintf(out, "\nApply %d change(s) to %s? [y/N] ", plan.Actionable(), plan.TargetPath)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !isAffirmative(line) {
			fmt.Fprintln(out, "Not applied; no changes were made.")
			return 1
		}
	}
	return writeImportPlan(out, plan, dir, exists, existing)
}

// importJSONOutcome emits the single machine-readable plan document for a
// --json run and returns its exit code.
func importJSONOutcome(out io.Writer, plan *interop.Plan, flags importFlags, interactive bool) int {
	approval := interop.Approval{
		Apply: flags.apply, Yes: flags.yes, JSONOut: flags.jsonOut,
		DryRun: flags.dryRun, Interactive: interactive,
	}
	decision, msg := approval.Decide()
	switch decision {
	case interop.DecisionRefused:
		payload, _ := plan.JSON(false)
		fmt.Fprintln(out, string(payload))
		fmt.Fprintf(os.Stderr, "import: %s\n", msg)
		return 1
	case interop.DecisionPreview:
		payload, _ := plan.JSON(false)
		fmt.Fprintln(out, string(payload))
		return 0
	default: // DecisionApproved — JSON never prompts
		if plan.Actionable() == 0 {
			payload, _ := plan.JSON(false)
			fmt.Fprintln(out, string(payload))
			return 0
		}
		// Apply to a buffer first: stdout only ever carries the final
		// JSON document, never half a write's chatter (writeImportPlan's
		// failures go to stderr, never into the JSON stream).
		var scratch bytes.Buffer
		dir, err := importPlanDir(plan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import: %v\n", err)
			return 1
		}
		exists, existing, err := importExisting(plan.TargetPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import: %v\n", err)
			return 1
		}
		if code := writeImportPlan(&scratch, plan, dir, exists, existing); code != 0 {
			return code
		}
		payload, _ := plan.JSON(true)
		fmt.Fprintln(out, string(payload))
		return 0
	}
}

// writeImportPlan performs the approved write: manifest merged, written
// (append for adds-only, canonical rewrite when a change forces it), and a
// one-line result plus the honest next step printed to out.
func writeImportPlan(out io.Writer, plan *interop.Plan, dir string, exists bool, existing *manifest.Manifest) int {
	if plan.Actionable() == 0 {
		fmt.Fprintln(out, "\nNothing to apply: every imported entry is already present in the manifest.")
		return 0
	}
	content, err := renderImportedManifest(plan, dir, exists, existing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "import: %v\n", err)
		return 1
	}
	if err := os.WriteFile(plan.TargetPath, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "import: write %s: %v\n", plan.TargetPath, err)
		return 1
	}
	add, change, unchanged := plan.Counts()
	fmt.Fprintf(out, "\n✓ Wrote %s (%d added, %d changed, %d unchanged; %d existing preserved)\n",
		plan.TargetPath, add, change, unchanged, plan.PreservedExisting)
	fmt.Fprintln(out, "Next: run `litespm lock` to resolve the new requires. Import does not install — run `litespm install` when you want it deployed.")
	return 0
}

// resolveImportTarget finds the manifest for dir (walking up, exactly like
// manifest.FindUp) or names the new file a create would write.
func resolveImportTarget(dir string) (string, bool, error) {
	if found, err := manifest.FindUp(dir); err == nil {
		return found, true, nil
	}
	return filepath.Join(dir, manifest.ManifestFileNames[0]), false, nil
}

// importExisting re-reads the target manifest at apply time (the plan may
// have been printed a moment earlier). A target that stopped existing
// between preview and approval is reported, not guessed around.
func importExisting(targetPath string) (bool, *manifest.Manifest, error) {
	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("stat %s: %w", targetPath, err)
	}
	m, err := manifest.Load(targetPath)
	if err != nil {
		return false, nil, fmt.Errorf("cannot merge into %s: %w", targetPath, err)
	}
	return true, m, nil
}

// importPlanDir returns the project directory the plan writes into.
func importPlanDir(plan *interop.Plan) (string, error) {
	dir := filepath.Dir(plan.TargetPath)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("project directory %s is unavailable: %v", dir, err)
	}
	return dir, nil
}

// renderImportedManifest produces the exact bytes the approved plan writes.
//
//	creates: build a fresh manifest and marshal it canonically.
//	extends (adds only): append [[requires]] blocks to the existing bytes,
//	  so hand-written comments and formatting survive byte-for-byte.
//	rewrites (a constraint changed): merge into the loaded manifest and
//	  marshal canonically — the preview already warned that comments and
//	  unrecognized lines are not preserved.
func renderImportedManifest(plan *interop.Plan, dir string, exists bool, existing *manifest.Manifest) (string, error) {
	// Collect the requires this import adds or changes.
	var adds, changes []manifest.Require
	for _, e := range plan.Entries {
		req := manifest.Require{ID: e.ID, Constraint: e.Constraint, Targets: e.Targets}
		switch e.Action {
		case interop.ActionAdd:
			adds = append(adds, req)
		case interop.ActionChange:
			changes = append(changes, req)
		}
	}

	if !exists {
		m := &manifest.Manifest{
			SchemaVersion: manifest.SchemaVersion,
			ProjectName:   importProjectName(dir),
			DefaultScope:  "project",
		}
		m.Requires = append(m.Requires, adds...)
		m.Requires = append(m.Requires, changes...)
		return m.MarshalCanonical(), nil
	}

	if plan.TargetState == interop.TargetExtend {
		original, err := os.ReadFile(plan.TargetPath)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", plan.TargetPath, err)
		}
		var b strings.Builder
		b.Write(original)
		if len(original) > 0 && original[len(original)-1] != '\n' {
			b.WriteByte('\n')
		}
		for _, req := range adds {
			b.WriteString(requireBlock(req))
		}
		return b.String(), nil
	}

	// Rewrite: merge changes in place, append adds, marshal canonically.
	merged := *existing
	merged.Requires = append([]manifest.Require(nil), existing.Requires...)
	for _, req := range changes {
		replaced := false
		for i := range merged.Requires {
			if merged.Requires[i].ID == req.ID {
				merged.Requires[i].Constraint = req.Constraint
				merged.Requires[i].Targets = req.Targets
				replaced = true
				break
			}
		}
		if !replaced {
			merged.Requires = append(merged.Requires, req)
		}
	}
	merged.Requires = append(merged.Requires, adds...)
	return merged.MarshalCanonical(), nil
}

// requireBlock renders one [[requires]] block in the manifest's own
// canonical grammar (mirrors manifest.MarshalCanonical's per-require
// output — strconv.Quote and the same array quoting), which Parse reads
// back identically. The round-trip test proves the mirror.
func requireBlock(r manifest.Require) string {
	var b strings.Builder
	b.WriteString("\n[[requires]]\n")
	fmt.Fprintf(&b, "id = %s\n", strconv.Quote(r.ID))
	if r.Constraint != "" {
		fmt.Fprintf(&b, "constraint = %s\n", strconv.Quote(r.Constraint))
	}
	if len(r.Targets) > 0 {
		qs := make([]string, 0, len(r.Targets))
		for _, t := range r.Targets {
			qs = append(qs, strconv.Quote(t))
		}
		fmt.Fprintf(&b, "targets = [%s]\n", strings.Join(qs, ", "))
	}
	return b.String()
}

// importProjectName derives a project name for a brand-new manifest from
// the directory, with a fallback for roots that cannot name themselves.
func importProjectName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	if base == "" || base == "." || base == string(filepath.Separator) || base == "/" || base == "\\" {
		return "imported-project"
	}
	return base
}

// ---- help registration -----------------------------------------------------
//
// TestHelpCoversEveryDispatchCase (help_test.go) requires every dispatched
// command to carry a complete help page, and its catalog lives in help.go —
// a file this command does not own. The page therefore registers itself
// from its own file: the completeness, grouping and template tests run over
// it exactly as they do over every hand-registered page.

var importHelpPage = commandHelp{
	Canonical: "import",
	Group:     "MANAGE",
	Purpose:   "Import a foreign manifest (skills-lock, server.json, marketplace, plugin) into litespm.yml.",
	Synopsis: []string{
		"litespm import <format> <path> [--project <dir>] [--json]",
		"litespm import <format> <path> --apply [--yes]",
	},
	Does: []string{
		"Parses the named file as untrusted input (size- and depth-bounded, strictly schema-validated), normalizes it to LiteSPM `requires`, and prints a plan: target file, entries added/changed/unchanged, the preserved foreign record and a Skipped/lossy section — before anything is written.",
		"Writes the project manifest only, and only after you approve the printed plan: a terminal y/N, or `--apply --yes` for scripts. Installing is the separate `litespm install` step; `litespm lock` resolves the new requires afterwards.",
	},
	DoesNot: []string{
		"Never executes or fetches anything the file names: `command` marketplace sources are rejected (LPSM-IMPORT-005), ids and paths with `..` or absolute are refused (LPSM-IMPORT-003), and the input is capped at 8 MiB (LPSM-IMPORT-001).",
		"Does not write litespm.lock and does not install: a lock entry must stay reproducible from the manifest plus the local catalog, and v1 is import-only (ARCH/32 §5).",
	},
	Examples: []string{
		"litespm import skills-lock ./skills-lock.json",
		"litespm import server.json ./server.json --project . --apply --yes",
	},
	SeeAlso: []string{"lock", "install", "copy"},
}

func init() {
	helpCatalog["import"] = importHelpPage
	for i, g := range helpGroups {
		if g.Label != "MANAGE" {
			continue
		}
		entries := make([]string, 0, len(g.Entries)+1)
		for _, name := range g.Entries {
			entries = append(entries, name)
			if name == "lock" {
				entries = append(entries, "import")
			}
		}
		if len(entries) == len(g.Entries) { // lock missing: append defensively
			entries = append(entries, "import")
		}
		helpGroups[i].Entries = entries
	}
}
