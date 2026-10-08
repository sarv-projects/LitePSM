# Resolver & Installation Engine

## 1. Pure Dependency Resolver

The dependency resolver (`internal/resolver`) is a pure, side-effect-free functional component. It reads cached catalog releases and produces an `InstallPlan` without performing disk writes or network calls.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        Resolver Execution Flow                         │
│                                                                        │
│   InstallRequest(listingId, versionConstraint, targetScope)            │
│         │                                                              │
│         ▼                                                              │
│   Select Exact Version ──► Bind Immutable Git Ref / Archive Digest     │
│         │                                                              │
│         ▼                                                              │
│   DFS Dependency Traversal with Visited Stack (Cycle Detection)        │
│         │                                                              │
│         ▼                                                              │
│   Constraint Intersection (Range vs Exact) ──► Conflict? -> Fail Closed│
│         │                                                              │
│         ▼                                                              │
│   Enumerate Declared Effects & Runtime Requirements                    │
│         │                                                              │
│         ▼                                                              │
│   Compute Preconditions & Pre-Existing Collisions                      │
│         │                                                              │
│         ▼                                                              │
│   RFC 8785 Canonical JSON Serialization -> SHA-256 planHash            │
│         │                                                              │
│         ▼                                                              │
│   Output: InstallPlan v2 (Immutable Contract)                          │
└────────────────────────────────────────────────────────────────────────┘
```

### 1.1 Cycle Detection & Diamond Dependency Resolution Algorithm
The resolver aggregates incoming constraints across all paths in the dependency DAG, verifies that a
non-empty version intersection exists, and ensures the selected version satisfies every parent
constraint. Entry point is `Resolver.Resolve(ctx, rootListingID, rootConstraintStr)`
(`internal/resolver/resolver.go:35`), returning `*domain.DependencyResolutionResult`; errors use
`domain.ErrResolveCycle` (`LPSM-RESOLVE-CYCLE`) and `domain.ErrResolveConflict`
(`LPSM-RESOLVE-CONFLICT`):

```go
// internal/resolver/resolver.go — the real surface
func NewResolver(provider ListingProvider) *Resolver
func (r *Resolver) Resolve(ctx context.Context, rootListingID string, rootConstraintStr string) (*domain.DependencyResolutionResult, error)

// unexported helper (topological sort of the resolved graph)
func computeTopologicalOrder(root string, graph map[string][]string) ([]string, error)
```

Earlier drafts of this section sketched helper methods (`collectConstraints`,
`selectBestVersionIntersect`) that do not exist in the package; the signature above is the
authoritative one.

### 1.2 Version-less listings resolve to the release's implicit pin (2026-10-08)

A listing that publishes no versions — `listing.Versions == []`, which is 5,811 of the 5,825
listings in `rel-2026-10-07-01` — used to fail inside `Resolve` with `LPSM-RESOLVE-CONFLICT`
("no versions available in catalog"). Recommendation A2 changed the resolver contract as follows:

*   **The pin is the release's existing implicit version**, `resolver.ImplicitVersion`
    (`"discovery"`, declared in `internal/resolver/semver.go`) — the literal
    `internal/catalogbuild/dataset.go` (`versionForID`) already embeds in every component id of
    such a listing (`<listing-id>@discovery#<kind>/<name>`), while the version record's own
    `version` stays `""`. Nothing is synthesized: no fabricated version (e.g. `0.0.0`) reaches
    `plan.Resolved.Version`, the plan hash, or an install record.
*   **It satisfies only constraints that do not name a different version:** the wildcards (empty,
    `*`, `latest`) and an explicit request for the literal — the `install --frozen` round trip of
    a locked version-less entry. A real semver range against a version-less listing still fails
    closed with `LPSM-RESOLVE-CONFLICT`; the pin compares below every published version
    (including `0.0.0`), so it can never satisfy `>=x.y.z`. A listing that does publish versions
    resolves exactly as before — the pin is never selected for it, and asking for it of a
    versioned listing is a conflict, not an alias for "latest".
*   **Catalog reads still use the published record.** The plan/lock adapter
    (`catalogRecordVersion` / `versionRecordFor` in `cmd/litespm/main.go`) translates the pin
    back to the empty version the release publishes the record under, so a version-less plan and
    a version-less lock entry read the catalog's real record (components, declared permissions)
    instead of degrading to "not knowable".
*   **Hash-persisted contract — nothing already persisted is invalidated.** A version-less
    listing could never produce an `InstallPlan` or a lock entry before this change (resolution
    always failed first), and every previously resolvable listing resolves to an identical
    selection, so every stored `planHash` still recomputes and every committed `litespm.lock`
    still passes `--check`. From this change `litespm lock` records `version: "discovery"` for a
    version-less manifest entry where it previously failed, and `install --frozen` replays that
    entry as an exact pin request.

---

## 2. Safe Extraction Pipeline

Once a plan is approved, the artifact engine (`internal/artifact`) verifies the archive hash, spools it safely to a bounded temporary file, and extracts the payload into an isolated staging directory. Entry points are `ExtractArchiveSafelyWithLimits` / `ExtractFileSafely` (`internal/artifact/extractor.go`); all safety rejections use `domain.ErrArchiveSlip` (`LPSM-CAS-ARCHIVE-SLIP`), not `LPSM-ARTIFACT-*`:

```go
// Sketch — follows internal/artifact/extractor.go:extractZip
func ExtractArchiveSafelyWithLimits(r io.ReaderAt, size int64, archiveType string, stagingDir string, limits ExtractionLimits) (*domain.ExtractedTreeInfo, error) {
    var totalBytes int64
    var fileCount int
    seenLower := make(map[string]string) // Case-fold collision detection

    zr, err := zip.NewReader(r, size)
    if err != nil {
        return nil, domain.ErrArchiveSlip("invalid or corrupt archive") // LPSM-CAS-ARCHIVE-SLIP
    }

    for _, f := range zr.File {
        fileCount++
        if fileCount > 20000 {
            return nil, domain.ErrArchiveSlip("archive exceeds max file count") // LPSM-CAS-ARCHIVE-SLIP
        }

        // Normalize slashes and backslashes
        normalized := filepath.ToSlash(f.Name)
        cleaned := filepath.Clean(normalized)

        // Reject absolute paths, relative parent traversal, and Windows drive/colon identifiers
        if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, ":") {
            return nil, domain.ErrArchiveSlip(fmt.Sprintf("unsafe path: %s", f.Name)) // LPSM-CAS-ARCHIVE-SLIP
        }

        // Check for case-fold collisions on case-insensitive filesystems (Windows/macOS)
        lowerPath := strings.ToLower(cleaned)
        if orig, exists := seenLower[lowerPath]; exists && orig != cleaned {
            return nil, domain.ErrArchiveSlip(fmt.Sprintf("case-fold collision: %s", cleaned)) // LPSM-CAS-ARCHIVE-SLIP
        }
        seenLower[lowerPath] = cleaned

        // Prohibit symlinks, hardlinks, and special device files
        if f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeNamedPipe != 0 || f.Mode()&os.ModeDevice != 0 {
            return nil, domain.ErrArchiveSlip("prohibited file type") // symlinks/FIFOs/devices/sockets — LPSM-CAS-ARCHIVE-SLIP
        }

        targetPath := filepath.Join(stagingDir, filepath.FromSlash(cleaned))
        // Boundary check: ensure target stays strictly within stagingDir
        if !strings.HasPrefix(targetPath, filepath.Clean(stagingDir)+string(filepath.Separator)) {
            return nil, domain.ErrArchiveSlip("path traversal escape") // LPSM-CAS-ARCHIVE-SLIP
        }

        if f.FileInfo().IsDir() {
            if err := os.MkdirAll(targetPath, 0755); err != nil {
                return nil, err
            }
            continue
        }

        // Enforce max single file size (128 MiB) and cumulative total size (1 GiB)
        if f.UncompressedSize64 > 128*1024*1024 {
            return nil, domain.ErrArchiveSlip("single file size limit exceeded") // LPSM-CAS-ARCHIVE-SLIP
        }
        totalBytes += int64(f.UncompressedSize64)
        if totalBytes > 1024*1024*1024 {
            return nil, domain.ErrArchiveSlip("total extracted size limit exceeded") // LPSM-CAS-ARCHIVE-SLIP
        }

        if err := writeBoundedFile(f, targetPath); err != nil {
            return nil, err
        }
    }

    // Compute canonical Merkle TreeDigest over the extracted filesystem tree
    canonicalTreeDigest, err := computeCanonicalTreeDigest(stagingDir)
    if err != nil {
        return nil, err
    }

    return &domain.ExtractedTreeInfo{
        FileCount:  fileCount,
        TotalBytes: totalBytes,
        TreeDigest: canonicalTreeDigest,
    }, nil
}
```

---

## 3. Two-Phase Atomic Commit Pipeline & Safe CAS Rollback

```text
Staging Directory (staging/<op-id>/)
        │
        ▼ 1. Compute Content-Addressed Tree Digest
DATA_ROOT/cas/trees/sha256/<64-hex-digest> (flat digest directory; created if
        missing, tracked in operation_trees). Not a two-level fan-out.
        │
        ▼ 2. Atomic Directory Rename (Same Filesystem)
Target Tree Ready
        │
        ▼ 3. SQLite Transaction Commit (Single ACID boundary)
   BEGIN TRANSACTION;
     INSERT INTO installs ...;
     INSERT INTO install_components ...;
     INSERT INTO providers ...;
     INSERT INTO operation_trees (operation_id, tree_digest, created_by_op) VALUES (?, ?, 1);
     UPDATE operations SET state = 'committed' WHERE operation_id = ?;
   COMMIT;
```

The tree path is computed by `Engine.TreePath` (`internal/install/engine.go:471-481`): it validates
a `sha256:<64-hex>` digest and joins `casRoot/trees/sha256/<hex>`. The CAS root itself is
`DATA_ROOT/cas` (`internal/config/paths.go:213`).

### Safe CAS Rollback Rules
If an error occurs before or during commit:
1.  **Check CAS Tree Ownership:** The engine queries `operation_trees` for `operation_id = ? AND created_by_op = 1`.
2.  **Shared Tree Preservation:** If the target tree already existed before this operation (`created_by_op = 0`), the engine **NEVER deletes** the tree directory in `trees/`.
3.  **Clean Staging:** The engine deletes `staging/<operation-id>`.
4.  **Audit Rollback:** Marks the operation journal record as `rolled_back`.
