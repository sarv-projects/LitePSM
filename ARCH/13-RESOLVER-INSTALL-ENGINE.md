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

### 1.1 Cycle Detection Algorithm
```go
func (r *Resolver) ResolveDependencies(ctx context.Context, root *domain.Listing, targetVer string) ([]domain.DependencyResolution, error) {
    resolved := make([]domain.DependencyResolution, 0)
    visited := make(map[string]bool)
    visiting := make(map[string]bool)

    var dfs func(listingID, verConstraint string) error
    dfs = func(listingID, verConstraint string) error {
        if visiting[listingID] {
            return domain.NewError("LPSM-RESOLVE-CYCLE", fmt.Sprintf("Circular dependency detected at %s", listingID))
        }
        if visited[listingID] {
            return nil
        }

        visiting[listingID] = true
        defer func() { visiting[listingID] = false }()

        versionRecord, err := r.selectBestVersion(ctx, listingID, verConstraint)
        if err != nil {
            return err
        }

        for _, dep := range versionRecord.Dependencies {
            if err := dfs(dep.ListingID, dep.VersionConstraint); err != nil {
                return err
            }
        }

        visited[listingID] = true
        resolved = append(resolved, domain.DependencyResolution{
            ListingID: listingID,
            Selected:  versionRecord,
        })
        return nil
    }

    if err := dfs(root.ID, targetVer); err != nil {
        return nil, err
    }
    return resolved, nil
}
```

---

## 2. Safe Extraction Pipeline

Once a plan is approved, the artifact engine (`internal/artifact`) downloads and extracts the software payload into an isolated staging directory:

```go
func (e *ArtifactEngine) ExtractArchiveSafely(reader io.Reader, stagingDir string) (*domain.ExtractedTreeInfo, error) {
    var totalBytes int64
    var fileCount int
    hasher := sha256.New()
    multiReader := io.TeeReader(reader, hasher)

    zr, err := zip.NewReader(multiReader, ...)
    if err != nil {
        return nil, domain.NewError("LPSM-ARTIFACT-MALFORMED", "Invalid archive format")
    }

    for _, f := range zr.File {
        fileCount++
        if fileCount > 20000 {
            return nil, domain.NewError("LPSM-ARTIFACT-LIMIT-EXCEEDED", "Archive exceeds max file count (20,000)")
        }

        // Clean and normalize target relative path
        cleaned := filepath.Clean(f.Name)
        if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, ":") {
            return nil, domain.NewError("LPSM-ARTIFACT-UNSAFE-PATH", fmt.Sprintf("Path traversal detected: %s", f.Name))
        }

        // Prohibit symlinks and special files
        if f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeNamedPipe != 0 {
            return nil, domain.NewError("LPSM-ARTIFACT-UNSUPPORTED-TYPE", "Symlinks and FIFOs are strictly forbidden")
        }

        targetPath := filepath.Join(stagingDir, cleaned)
        // Ensure path stays within stagingDir
        if !strings.HasPrefix(targetPath, filepath.Clean(stagingDir)+string(filepath.Separator)) {
            return nil, domain.NewError("LPSM-ARTIFACT-PATH-ESCAPE", "Extracted path escapes root")
        }

        if f.FileInfo().IsDir() {
            if err := os.MkdirAll(targetPath, 0755); err != nil {
                return nil, err
            }
            continue
        }

        // Enforce max single file size (128 MiB) and total size (1 GiB)
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

    return &domain.ExtractedTreeInfo{
        FileCount:  fileCount,
        TotalBytes: totalBytes,
        TreeDigest: fmt.Sprintf("sha256:%x", hasher.Sum(nil)),
    }, nil
}
```

---

## 3. Two-Phase Atomic Commit Pipeline

```text
Staging Directory (staging/<op-id>/)
        │
        ▼ 1. Compute Content-Addressed Tree Digest
trees/sha256/a1/a1b2c3d4...
        │
        ▼ 2. Atomic Directory Rename (Same Filesystem)
Target Tree Ready
        │
        ▼ 3. SQLite Transaction Commit (Single ACID boundary)
   BEGIN TRANSACTION;
     INSERT INTO installs ...;
     INSERT INTO install_components ...;
     INSERT INTO providers ...;
     UPDATE operations SET state = 'committed' WHERE operation_id = ?;
   COMMIT;
```

If an error occurs before the SQLite transaction commits, the daemon executes a rollback:
1.  Removes the newly created tree directory in `trees/`.
2.  Deletes the staging directory.
3.  Marks the operation journal record as `rolled_back`.
