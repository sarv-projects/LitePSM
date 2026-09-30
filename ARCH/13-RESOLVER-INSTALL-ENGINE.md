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
The resolver aggregates incoming constraints across all paths in the dependency DAG, verifies that a non-empty version intersection exists, and ensures the selected version satisfies every parent constraint:

```go
type VersionConstraints struct {
    ListingID   string
    Constraints []string
}

func (r *Resolver) ResolveDependencies(ctx context.Context, root *domain.Listing, targetVer string) ([]domain.DependencyResolution, error) {
    resolved := make([]domain.DependencyResolution, 0)
    selected := make(map[string]*domain.VersionRecord)
    aggregatedConstraints := make(map[string][]string)
    visiting := make(map[string]bool)

    // Phase 1: Collect and intersect all dependency constraints across the graph
    var collectConstraints func(listingID, verConstraint string) error
    collectConstraints = func(listingID, verConstraint string) error {
        if visiting[listingID] {
            return domain.NewError("LPSM-RESOLVE-CYCLE", fmt.Sprintf("Circular dependency detected at %s", listingID))
        }

        aggregatedConstraints[listingID] = append(aggregatedConstraints[listingID], verConstraint)

        visiting[listingID] = true
        defer func() { visiting[listingID] = false }()

        // Select or verify candidate version against ALL accumulated constraints
        candidate, err := r.selectBestVersionIntersect(ctx, listingID, aggregatedConstraints[listingID])
        if err != nil {
            return domain.NewError("LPSM-RESOLVE-CONFLICT", 
                fmt.Sprintf("Constraint conflict for %s across incoming requirements %v: %v", 
                    listingID, aggregatedConstraints[listingID], err))
        }
        selected[listingID] = candidate

        for _, dep := range candidate.Dependencies {
            if err := collectConstraints(dep.ListingID, dep.VersionConstraint); err != nil {
                return err
            }
        }
        return nil
    }

    if err := collectConstraints(root.ID, targetVer); err != nil {
        return nil, err
    }

    // Phase 2: Produce topologically sorted resolution slice
    for id, verRec := range selected {
        resolved = append(resolved, domain.DependencyResolution{
            ListingID: id,
            Selected:  verRec,
        })
    }
    return resolved, nil
}
```

---

## 2. Safe Extraction Pipeline

Once a plan is approved, the artifact engine (`internal/artifact`) verifies the archive hash, spools it safely to a bounded temporary file, and extracts the payload into an isolated staging directory using an `io.ReaderAt`:

```go
func (e *ArtifactEngine) ExtractArchiveSafely(r io.ReaderAt, size int64, stagingDir string) (*domain.ExtractedTreeInfo, error) {
    var totalBytes int64
    var fileCount int
    seenLower := make(map[string]string) // Case-fold collision detection

    zr, err := zip.NewReader(r, size)
    if err != nil {
        return nil, domain.NewError("LPSM-ARTIFACT-MALFORMED", "Invalid archive format")
    }

    for _, f := range zr.File {
        fileCount++
        if fileCount > 20000 {
            return nil, domain.NewError("LPSM-ARTIFACT-LIMIT-EXCEEDED", "Archive exceeds max file count (20,000)")
        }

        // Normalize slashes and backslashes
        normalized := filepath.ToSlash(f.Name)
        cleaned := filepath.Clean(normalized)

        // Reject absolute paths, relative parent traversal, and Windows drive/colon identifiers
        if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, ":") {
            return nil, domain.NewError("LPSM-ARTIFACT-UNSAFE-PATH", fmt.Sprintf("Path traversal or illegal identifier detected: %s", f.Name))
        }

        // Check for case-fold collisions on case-insensitive filesystems (Windows/macOS)
        lowerPath := strings.ToLower(cleaned)
        if orig, exists := seenLower[lowerPath]; exists && orig != cleaned {
            return nil, domain.NewError("LPSM-ARTIFACT-COLLISION", fmt.Sprintf("Case-fold collision detected: %s collides with %s", cleaned, orig))
        }
        seenLower[lowerPath] = cleaned

        // Prohibit symlinks, hardlinks, and special device files
        if f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeNamedPipe != 0 || f.Mode()&os.ModeDevice != 0 {
            return nil, domain.NewError("LPSM-ARTIFACT-UNSUPPORTED-TYPE", "Symlinks, FIFOs, and devices are strictly forbidden")
        }

        targetPath := filepath.Join(stagingDir, filepath.FromSlash(cleaned))
        // Boundary check: ensure target stays strictly within stagingDir
        if !strings.HasPrefix(targetPath, filepath.Clean(stagingDir)+string(filepath.Separator)) {
            return nil, domain.NewError("LPSM-ARTIFACT-PATH-ESCAPE", "Extracted path escapes staging root")
        }

        if f.FileInfo().IsDir() {
            if err := os.MkdirAll(targetPath, 0755); err != nil {
                return nil, err
            }
            continue
        }

        // Enforce max single file size (128 MiB) and cumulative total size (1 GiB)
        if f.UncompressedSize64 > 128*1024*1024 {
            return nil, domain.NewError("LPSM-ARTIFACT-LIMIT-EXCEEDED", "File exceeds max single file limit (128 MiB)")
        }
        totalBytes += int64(f.UncompressedSize64)
        if totalBytes > 1024*1024*1024 {
            return nil, domain.NewError("LPSM-ARTIFACT-LIMIT-EXCEEDED", "Total uncompressed size exceeds limit (1 GiB)")
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
trees/sha256/a1/a1b2c3d4... (Created if missing, tracked in operation_trees)
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

### Safe CAS Rollback Rules
If an error occurs before or during commit:
1.  **Check CAS Tree Ownership:** The engine queries `operation_trees` for `operation_id = ? AND created_by_op = 1`.
2.  **Shared Tree Preservation:** If the target tree already existed before this operation (`created_by_op = 0`), the engine **NEVER deletes** the tree directory in `trees/`.
3.  **Clean Staging:** The engine deletes `staging/<operation-id>`.
4.  **Audit Rollback:** Marks the operation journal record as `rolled_back`.
