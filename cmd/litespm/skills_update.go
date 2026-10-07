package main

// skills_update.go — `litespm skills update`.
//
// `skills add` records what it wrote (source, digest, inventory). Update is the
// counterpart that refreshes an installed skill from a freshly fetched source
// while keeping the install ledger and the on-disk tree consistent. Fetching is
// done here (git clone or a local directory); the skills package performs the
// policy consult and the atomic swap.
//
// Honest results: an unchanged source reports "already current", a --dry-run
// reports "would update" without touching disk, a scope mismatch is reported
// and skipped, and a source that does not contain the named skill is an error,
// never a silent no-op.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/skills"
)

func skillsUpdateUsage() {
	fmt.Println(`Usage: litespm skills update <name>... --source <src> [flags]

Update one or more installed skills from a source. Each named skill is matched
against the install ledger, then against the skills discovered in the source.

  <name>...                 one or more installed skill names
  --source, -s <src>        owner/repo, https git URL, or local directory
  --ref, -r <r>             git branch/tag to fetch (remote sources only)
  --scope <project|global>  only update ledger entries in this scope
  --global, -g              shorthand for --scope global
  --force, -f               replace a directory even if it has user-added files
  --dry-run, -n             report what would change without writing
  --yes, -y                 approve a policy "ask" non-interactively
  --json                    machine-readable output on stdout

Run ` + "`litespm skills list`" + ` to see what the ledger holds.`)
}

type skillsUpdateOptions struct {
	names      []string
	source     string
	ref        string
	scope      string
	scopeGiven bool
	force      bool
	dryRun     bool
	jsonOut    bool
	yes        bool
	help       bool
}

func parseSkillsUpdateArgs(args []string) (skillsUpdateOptions, error) {
	var opts skillsUpdateOptions
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
		case "--source", "-s":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.source = v
		case "--ref", "-r":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.ref = v
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
		case "--force", "-f":
			opts.force = true
		case "--dry-run", "-n":
			opts.dryRun = true
		case "--json":
			opts.jsonOut = true
		case "--yes", "-y":
			opts.yes = true
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
	return opts, nil
}

// skillUpdateResult is one honest per-entry outcome.
type skillUpdateResult struct {
	Name          string `json:"name"`
	Host          string `json:"host,omitempty"`
	Dir           string `json:"dir,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	ContentDigest string `json:"contentDigest,omitempty"`
	SourceRef     string `json:"sourceRef,omitempty"`
}

func runSkillsUpdate(args []string) {
	opts, err := parseSkillsUpdateArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		skillsUpdateUsage()
		os.Exit(1)
	}
	if opts.help {
		skillsUpdateUsage()
		return
	}
	if strings.TrimSpace(opts.source) == "" {
		fmt.Fprintln(os.Stderr, "Error: --source <src> is required")
		skillsUpdateUsage()
		os.Exit(1)
	}
	if len(opts.names) == 0 {
		fmt.Fprintln(os.Stderr, "Error: at least one skill <name> is required")
		skillsUpdateUsage()
		os.Exit(1)
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	results, err := executeSkillsUpdate(context.Background(), opts, paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if opts.jsonOut {
		payload, _ := json.Marshal(map[string]any{
			"source":  opts.source,
			"dryRun":  opts.dryRun,
			"results": results,
		})
		fmt.Println(string(payload))
	} else {
		printUpdateResults(results)
	}

	if n := skillUpdateFailures(results); n > 0 {
		os.Exit(1)
	}
}

// executeSkillsUpdate performs the work and returns one result per matched
// ledger entry (plus a not_installed result per requested name with no entry).
// A returned error is fatal (ledger/source cannot be read) and is distinct from
// per-entry failures, which are reported in the results.
func executeSkillsUpdate(ctx context.Context, opts skillsUpdateOptions, paths *config.PlatformPaths) ([]skillUpdateResult, error) {
	ledger, err := skills.OpenLedger(skills.LedgerPath(paths.DataRoot))
	if err != nil {
		return nil, err
	}
	entries, err := ledger.All()
	if err != nil {
		return nil, fmt.Errorf("reading the install ledger: %w", err)
	}

	src, err := skills.ParseSkillSource(opts.source)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.ref) != "" {
		if src.Kind == "local" {
			return nil, fmt.Errorf("--ref applies only to git sources; %s is a local directory", src.Display)
		}
		src.Pin = strings.TrimSpace(opts.ref)
	}

	workRoot, cleanup, err := fetchSkillSourceTree(src)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	commit := ""
	if src.Kind != "local" {
		commit = gitHeadCommit(workRoot)
	}
	newRef := src.RecordedRef(commit)

	found, err := discoverSkillsIncludingRoot(workRoot)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]string, len(found))
	for _, f := range found {
		name, err := skills.SanitizeSkillName(f.Pkg.Name)
		if err != nil {
			continue
		}
		if _, dup := byName[name]; !dup {
			byName[name] = f.Dir
		}
	}

	asker := newInteractivePolicyAsker(opts.yes)
	policyEngine, closePolicy, policyErr := openCLIPolicyEngine(paths.DataRoot)
	if policyErr != nil {
		return nil, fmt.Errorf("load policy: %w", policyErr)
	}
	defer closePolicy()
	installer := skills.NewInstaller(ledger, enginePolicyChecker(policyEngine, asker.resolve))

	wanted := make([]string, 0, len(opts.names))
	seen := make(map[string]bool, len(opts.names))
	for _, raw := range opts.names {
		name, err := skills.SanitizeSkillName(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid skill name %q", raw)
		}
		if !seen[name] {
			seen[name] = true
			wanted = append(wanted, name)
		}
	}

	var results []skillUpdateResult
	matched := map[string]bool{}
	for _, e := range entries {
		if !seen[e.SkillName] {
			continue
		}
		matched[e.SkillName] = true

		if opts.scopeGiven && e.Scope != opts.scope {
			results = append(results, skillUpdateResult{
				Name:   e.SkillName,
				Host:   e.HostLabel,
				Dir:    e.DestDir,
				Status: "skipped",
				Reason: fmt.Sprintf("scope mismatch: installed scope %q, requested %q", e.Scope, opts.scope),
			})
			continue
		}

		dir := byName[e.SkillName]
		if dir == "" {
			results = append(results, skillUpdateResult{
				Name:   e.SkillName,
				Host:   e.HostLabel,
				Dir:    e.DestDir,
				Status: "error",
				Reason: "the source does not contain a skill named " + e.SkillName,
			})
			continue
		}

		outcome, err := installer.Update(ctx, skills.UpdateRequest{
			Entry:     e,
			NewDir:    dir,
			NewSource: src.Display,
			NewRef:    newRef,
			Force:     opts.force,
			DryRun:    opts.dryRun,
		})
		if err != nil {
			results = append(results, skillUpdateResult{
				Name:   e.SkillName,
				Host:   e.HostLabel,
				Dir:    e.DestDir,
				Status: "error",
				Reason: err.Error(),
			})
			continue
		}

		res := skillUpdateResult{
			Name:          outcome.Entry.SkillName,
			Host:          outcome.Entry.HostLabel,
			Dir:           outcome.Entry.DestDir,
			Reason:        outcome.Reason,
			ContentDigest: outcome.Entry.ContentDigest,
			SourceRef:     outcome.Entry.SourceRef,
		}
		switch {
		case outcome.Updated:
			res.Status = "updated"
		case outcome.WouldUpdate:
			res.Status = "would_update"
		case outcome.AlreadyCurrent:
			res.Status = "already_current"
		default:
			res.Status = "no_change"
		}
		results = append(results, res)
	}

	for _, name := range wanted {
		if !matched[name] {
			results = append(results, skillUpdateResult{
				Name:   name,
				Status: "not_installed",
				Reason: "no ledger entry for this skill",
			})
		}
	}
	return results, nil
}

func skillUpdateFailures(results []skillUpdateResult) int {
	n := 0
	for _, r := range results {
		if r.Status == "error" || r.Status == "not_installed" {
			n++
		}
	}
	return n
}

func printUpdateResults(results []skillUpdateResult) {
	if len(results) == 0 {
		fmt.Println("Nothing to update: no matching ledger entries.")
		return
	}
	var updated, would, current, skipped, failed int
	for _, r := range results {
		switch r.Status {
		case "updated":
			updated++
			fmt.Printf("  ✓ %-20s updated %s\n", r.Name, r.Dir)
		case "would_update":
			would++
			fmt.Printf("  - %-20s would update %s (%s)\n", r.Name, r.Dir, r.Reason)
		case "already_current":
			current++
			fmt.Printf("  = %-20s already current\n", r.Name)
		case "skipped":
			skipped++
			fmt.Printf("  ○ %-20s skipped: %s\n", r.Name, r.Reason)
		case "not_installed":
			failed++
			fmt.Printf("  ! %-20s not installed: %s\n", r.Name, r.Reason)
		default:
			failed++
			fmt.Printf("  ! %-20s failed: %s\n", r.Name, r.Reason)
		}
	}
	fmt.Println()
	fmt.Printf("%d updated · %d would update · %d already current · %d skipped · %d failed\n",
		updated, would, current, skipped, failed)
}

// fetchSkillSourceTree materializes a source into a local directory. Remote
// sources are shallow-cloned (honoring a branch/tag pin); local sources are
// used in place and need no cleanup.
func fetchSkillSourceTree(src skills.SkillSource) (root string, cleanup func(), err error) {
	if src.Kind == "local" {
		return src.LocalDir, func() {}, nil
	}
	if _, lookErr := exec.LookPath("git"); lookErr != nil {
		return "", nil, fmt.Errorf("git is required to fetch remote skill sources")
	}
	tmp, err := os.MkdirTemp("", "litespm-skills-update-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	args := []string{"clone", "--depth", "1"}
	if strings.TrimSpace(src.Pin) != "" {
		args = append(args, "--branch", src.Pin)
	}
	args = append(args, src.CloneURL, tmp)
	if out, cloneErr := exec.Command("git", args...).CombinedOutput(); cloneErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to clone %s: %v\n%s", src.Display, cloneErr, strings.TrimSpace(string(out)))
	}
	return tmp, cleanup, nil
}

// gitHeadCommit returns the checked-out commit of a git tree, or "" when the
// tree is not a git repository.
func gitHeadCommit(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// discoverSkillsIncludingRoot discovers skills under root and also accepts a
// source that is itself a single skill directory (SKILL.md at the root), which
// DiscoverSkills intentionally skips.
func discoverSkillsIncludingRoot(root string) ([]skills.DiscoveredSkill, error) {
	found, err := skills.DiscoverSkills(root)
	if err != nil {
		return nil, err
	}
	if len(found) > 0 {
		return found, nil
	}
	pkg, loadErr := skills.LoadSkillFromDirectory(root)
	if loadErr != nil {
		return found, nil
	}
	if strings.TrimSpace(pkg.Name) == "" || strings.TrimSpace(pkg.Description) == "" {
		return found, nil
	}
	return []skills.DiscoveredSkill{{Dir: root, Rel: ".", Pkg: pkg}}, nil
}
