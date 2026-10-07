package main

// lock.go — `litespm lock` and the `--frozen` install gate (ARCH/32 §4).
//
//	 litespm lock                 resolve the manifest, write litespm.lock
//	 litespm lock --check         recompute without writing; non-zero on drift (CI gate)
//	 litespm lock --sbom <format> emit an SBOM derived from the lock
//	 litespm lock --verify        re-verify lock digest + manifest binding, report per entry
//	 litespm install --frozen     install exactly the locked version, no resolution
//
// Determinism: lock and --check read only the authored manifest and the LOCAL
// synced catalog index (the same cache `catalog sync` writes); the resolver
// closure below injects no time, hostname or network state, so the same inputs
// always produce a byte-identical litespm.lock (ARCH/32 invariant 1).
//
// Honesty: fields the catalog does not publish — artifact bytes, CAS tree
// digest, policy/plan digests, schema fingerprints — are left empty, and
// lockfile.Freeze records signature none/unavailable plus license NOASSERTION
// unless a real check produced a value (ARCH/32 invariant 5). `--verify`
// repeats the recorded result; it never upgrades an entry to green.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/lockfile"
	"github.com/sarv-projects/litespm/internal/manifest"
	"github.com/sarv-projects/litespm/internal/resolver"
	"github.com/sarv-projects/litespm/internal/trust"
)

// lockFlags holds the parsed command line for `litespm lock`.
type lockFlags struct {
	check    bool
	verify   bool
	sbom     string // cyclonedx-json | spdx-json; empty = not requested
	showHelp bool
}

// parseLockFlags parses `litespm lock` arguments strictly: unknown flags,
// positional arguments, a missing --sbom value, an unsupported SBOM format
// and mutually exclusive modes are all usage errors (the caller exits 2).
// `--help`/`-h` is reported separately so it can exit 0.
func parseLockFlags(args []string) (lockFlags, error) {
	var f lockFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			f.showHelp = true
			return f, nil
		case "--check":
			f.check = true
		case "--verify":
			f.verify = true
		case "--sbom":
			if i+1 >= len(args) {
				return f, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			f.sbom = args[i]
		default:
			return f, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	modes := 0
	if f.check {
		modes++
	}
	if f.verify {
		modes++
	}
	if f.sbom != "" {
		modes++
	}
	if modes > 1 {
		return f, fmt.Errorf("--check, --sbom and --verify are mutually exclusive")
	}
	if f.sbom != "" && f.sbom != "cyclonedx-json" && f.sbom != "spdx-json" {
		return f, fmt.Errorf("--sbom format %q must be cyclonedx-json or spdx-json", f.sbom)
	}
	return f, nil
}

func lockUsage() {
	fmt.Println(`Usage:
  litespm lock                 resolve the manifest and write litespm.lock beside it
  litespm lock --check         recompute without writing; exit non-zero on drift (CI gate)
  litespm lock --sbom <format> emit an SBOM derived from the lock (cyclonedx-json|spdx-json)
  litespm lock --verify        re-verify the lock digest and manifest binding, report each entry

The lock is deterministic: the same litespm.yml and the same local catalog
index always produce a byte-identical litespm.lock. Commit the lock and run
"litespm lock --check" in CI to catch a manifest or catalog that moved without
a re-lock.

Honesty rule: entries record signature none / result unavailable and license
NOASSERTION until a real check produces a value, and fields the catalog does
not publish (artifact hashes, tree/policy/plan digests) stay empty — they are
never invented. "litespm lock --verify" reports exactly what is recorded.

With "litespm install --frozen <id>" the lock becomes the only source of the
version: no resolution, no solving, no silent add/remove. A missing, stale or
edited lock fails with LPSM-LOCK-DRIFT instead of repairing itself.`)
}

// newLockCatalogClient builds a catalog client over the LOCAL synced index
// (cache root = the platform data root, exactly like `litespm install`). The
// constructor only loads what `catalog sync` already wrote; nothing here
// fetches the network.
func newLockCatalogClient() (*catalog.Client, error) {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}
	cfg, _ := config.LoadCurrentConfig()
	return catalog.NewClientWithTimeout(registryURLOf(cfg), paths.DataRoot, catalogTimeoutOf(cfg), nil), nil
}

// newLockResolver adapts the local catalog index to lockfile.Resolver. Every
// field it fills is one the catalog actually publishes; everything else is
// left for lockfile.Freeze to default honestly.
func newLockResolver(ctx context.Context, catClient *catalog.Client) lockfile.Resolver {
	r := resolver.NewResolver(&catalogResolutionProvider{client: catClient})
	return func(id, constraint string) (lockfile.LockedEntry, error) {
		listing, err := catClient.GetListing(id)
		if err != nil {
			return lockfile.LockedEntry{}, fmt.Errorf("listing %s is not in the local catalog index (run `litespm catalog sync`): %w", id, err)
		}
		res, err := r.Resolve(ctx, id, constraint)
		if err != nil {
			return lockfile.LockedEntry{}, err
		}
		version := res.SelectedVersions[id]
		if strings.TrimSpace(version) == "" {
			return lockfile.LockedEntry{}, fmt.Errorf("resolver selected no version for %s", id)
		}
		entry := lockfile.LockedEntry{
			ID:             id,
			Version:        version,
			SourceIdentity: strings.TrimSpace(listing.Source.SourceID),
			SourceURL:      strings.TrimSpace(listing.Source.URL),
			PublisherName:  strings.TrimSpace(listing.PublisherClaim.Name),
			// Constraint, Targets: set by lockfile.Freeze from the manifest.
			// Signature*, License: left empty so Freeze records none/unavailable
			// and NOASSERTION. Artifact*, TreeDigest, PolicyDigest, PlanDigest,
			// Commit: left empty — the local index publishes none of them.
		}
		// Transport and components come from the published version record when
		// the synced index carries one; an index without version records
		// publishes neither, so both stay empty rather than defaulted.
		if rec := versionRecordFor(catClient, id, version); rec != nil {
			for _, comp := range rec.Components {
				if cid := strings.TrimSpace(string(comp.ID)); cid != "" {
					entry.Components = append(entry.Components, cid)
				}
				if entry.Transport == "" && comp.Runtime != nil {
					entry.Transport = strings.TrimSpace(comp.Runtime.Type)
				}
			}
		}
		// Fall back to the listing's component summary: canonical ComponentIDs
		// derived from published kind+name+version, never free text.
		if len(entry.Components) == 0 {
			for _, cs := range listing.ComponentsSummary {
				if cs.Kind == "" || strings.TrimSpace(cs.Name) == "" {
					continue
				}
				cid := string(domain.NewComponentID(domain.ListingID(listing.ID), version, cs.Kind, cs.Name))
				entry.Components = append(entry.Components, cid)
			}
		}
		return entry, nil
	}
}

// freezeProjectLock resolves the manifest's requirements against the local
// catalog into a lock. No write happens here — callers decide.
func freezeProjectLock(ctx context.Context, m *manifest.Manifest, catClient *catalog.Client) (*lockfile.Lock, error) {
	return lockfile.Freeze(m, newLockResolver(ctx, catClient))
}

// checkProjectLock recomputes the lock WITHOUT writing and compares it with
// the committed bytes: identical passes, any difference (edited lock, changed
// manifest, changed catalog) or a missing lock is an error — the CI gate.
func checkProjectLock(ctx context.Context, m *manifest.Manifest, catClient *catalog.Client, lockPath string) error {
	recomputed, err := freezeProjectLock(ctx, m, catClient)
	if err != nil {
		return err
	}
	committed, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("LPSM-LOCK-DRIFT: no %s at %s; run \"litespm lock\" to create it", lockfile.LockFileName, lockPath)
		}
		return fmt.Errorf("read %s: %w", lockPath, err)
	}
	if string(committed) == recomputed.MarshalCanonical() {
		return nil
	}
	// Distinguish a hand-edited lock (digest broken) from a recomputed
	// difference (inputs moved) so the CI message says which happened.
	if parsed, perr := lockfile.Parse(committed); perr == nil {
		if verr := parsed.Verify(); verr != nil {
			return fmt.Errorf("LPSM-LOCK-DRIFT: %s was edited after it was generated (%v); run \"litespm lock\" and review the diff",
				filepath.Base(lockPath), verr)
		}
	}
	return fmt.Errorf("LPSM-LOCK-DRIFT: %s differs from the lock recomputed from %s (manifest or local catalog changed); run \"litespm lock\" and commit the result",
		filepath.Base(lockPath), filepath.Base(m.SourcePath))
}

// sbomFromLock loads the committed lock (digest-verified) and derives an SBOM
// from its resolved[] entries (ARCH/32 §4.1). The document carries the lock
// digest it was generated from, so the two cannot drift.
func sbomFromLock(lockPath, format string) ([]byte, error) {
	l, err := lockfile.Load(lockPath)
	if err != nil {
		return nil, err
	}
	return trust.SBOM(l, format)
}

// verifyProjectLock re-verifies the lock digest and the manifest binding, then
// reports one line per entry stating the recorded signature result honestly:
// unavailable stays unavailable, and a non-none scheme is labeled as a
// recorded value that this command did not re-check.
func verifyProjectLock(manifestPath, lockPath string) ([]string, error) {
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, err
	}
	l, err := lockfile.Load(lockPath) // Load recomputes and checks the digest
	if err != nil {
		return nil, err
	}
	if err := l.Verify(); err != nil {
		return nil, err
	}
	if err := l.CheckManifest(m); err != nil {
		return nil, err
	}
	lines := []string{
		fmt.Sprintf("lock digest %s verified", l.LockDigest),
		fmt.Sprintf("manifest digest %s matches %s", l.ManifestDigest, filepath.Base(manifestPath)),
	}
	if len(l.Entries) == 0 {
		lines = append(lines, "  (no resolved entries)")
	}
	for _, e := range l.Entries {
		line := fmt.Sprintf("  %s %s  signature=%s  result=%s  license=%s",
			e.ID, e.Version, e.SignatureScheme, e.SignatureResult, e.License)
		if e.SignatureScheme != lockfile.SchemeNone {
			line += "  (recorded value; no signature material re-checked by --verify)"
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// asLockDrift guarantees a --frozen failure carries the LPSM-LOCK-DRIFT code
// without double-prefixing errors that already have it (lockfile.Verify and
// CheckManifest produce it themselves).
func asLockDrift(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "LPSM-LOCK-DRIFT") {
		return err
	}
	return fmt.Errorf("LPSM-LOCK-DRIFT: %w", err)
}

// frozenVersionFor is the resolution step of `litespm install --frozen`: it
// returns the exact version pinned in litespm.lock for listingID and refuses
// (LPSM-LOCK-DRIFT) when the lock is missing, fails its digest, is stale
// relative to the manifest, does not contain the listing, or disagrees with an
// explicitly requested version. It reads only the manifest and the lock — no
// catalog, no resolution, no network (ARCH/32 invariant 2).
func frozenVersionFor(dir, listingID, requestedVersion string) (string, error) {
	manifestPath, err := manifest.FindUp(dir)
	if err != nil {
		return "", fmt.Errorf("LPSM-LOCK-DRIFT: cannot validate a frozen install without a project manifest: %v", err)
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return "", asLockDrift(fmt.Errorf("cannot read manifest %s: %v", manifestPath, err))
	}
	lockPath, err := lockfile.FindUp(filepath.Dir(manifestPath))
	if err != nil {
		return "", asLockDrift(fmt.Errorf("no %s found from %s; run \"litespm lock\" first: %v",
			lockfile.LockFileName, filepath.Dir(manifestPath), err))
	}
	l, err := lockfile.Load(lockPath) // digest verified inside Load; its error carries the path
	if err != nil {
		return "", asLockDrift(err)
	}
	if err := l.CheckManifest(m); err != nil {
		return "", asLockDrift(err)
	}
	entry, ok := l.Lookup(listingID)
	if !ok {
		return "", fmt.Errorf("LPSM-LOCK-DRIFT: listing %s is not in %s; a frozen install never adds a package — run \"litespm lock\" to record it",
			listingID, lockfile.LockFileName)
	}
	if strings.TrimSpace(entry.Version) == "" {
		return "", fmt.Errorf("LPSM-LOCK-DRIFT: locked entry for %s carries no version", listingID)
	}
	if requestedVersion != "" && requestedVersion != entry.Version {
		return "", fmt.Errorf("LPSM-LOCK-DRIFT: requested version %s but %s is locked at %s; --frozen never overrides the lock",
			requestedVersion, listingID, entry.Version)
	}
	return entry.Version, nil
}

// runLock is the CLI entry point for `litespm lock` and its modes.
func runLock(ctx context.Context, args []string) {
	flags, err := parseLockFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Lock error: %v\n\n", err)
		lockUsage()
		os.Exit(2)
	}
	if flags.showHelp {
		lockUsage()
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Lock failed: resolve working directory: %v\n", err)
		os.Exit(1)
	}

	switch {
	case flags.sbom != "":
		// SBOM derives from the committed lock only — no manifest needed.
		lockPath, ferr := lockfile.FindUp(cwd)
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "SBOM failed: %v; run \"litespm lock\" first\n", ferr)
			os.Exit(1)
		}
		doc, serr := sbomFromLock(lockPath, flags.sbom)
		if serr != nil {
			fmt.Fprintf(os.Stderr, "SBOM failed: %v\n", serr)
			os.Exit(1)
		}
		fmt.Println(string(doc))

	case flags.verify:
		manifestPath, ferr := manifest.FindUp(cwd)
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "Verify failed: %v\n", ferr)
			os.Exit(1)
		}
		lockPath, ferr := lockfile.FindUp(filepath.Dir(manifestPath))
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "Verify failed: %v; run \"litespm lock\" first\n", ferr)
			os.Exit(1)
		}
		lines, verr := verifyProjectLock(manifestPath, lockPath)
		if verr != nil {
			fmt.Fprintf(os.Stderr, "Verify failed: %v\n", verr)
			os.Exit(1)
		}
		for _, line := range lines {
			fmt.Println(line)
		}

	default: // write, or --check
		manifestPath, ferr := manifest.FindUp(cwd)
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "Lock failed: %v\n", ferr)
			os.Exit(1)
		}
		m, merr := manifest.Load(manifestPath)
		if merr != nil {
			fmt.Fprintf(os.Stderr, "Lock failed: %v\n", merr)
			os.Exit(1)
		}
		catClient, cerr := newLockCatalogClient()
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "Lock failed: %v\n", cerr)
			os.Exit(1)
		}

		if flags.check {
			lockPath, ferr := lockfile.FindUp(filepath.Dir(manifestPath))
			if ferr != nil {
				fmt.Fprintf(os.Stderr, "Lock check failed: LPSM-LOCK-DRIFT: %v; run \"litespm lock\" first\n", ferr)
				os.Exit(1)
			}
			if err := checkProjectLock(ctx, m, catClient, lockPath); err != nil {
				fmt.Fprintf(os.Stderr, "Lock check failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✓ %s is up to date with %s and the local catalog index\n",
				filepath.Base(lockPath), filepath.Base(manifestPath))
			return
		}

		lockPath := filepath.Join(filepath.Dir(manifestPath), lockfile.LockFileName)
		l, err := freezeProjectLock(ctx, m, catClient)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Lock failed: %v\n", err)
			os.Exit(1)
		}
		if err := l.WriteFile(lockPath); err != nil {
			fmt.Fprintf(os.Stderr, "Lock failed: write %s: %v\n", lockPath, err)
			os.Exit(1)
		}
		fmt.Printf("✓ Wrote %s (%d resolved entries, lockDigest %s)\n",
			lockPath, len(l.Entries), l.LockDigest)
	}
}
