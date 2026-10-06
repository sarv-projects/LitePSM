package catalogbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VerifyMaterializedTree re-checks a release tree that has already been written
// to disk against its own pointer, using the same digest chain the catalog
// client enforces when it syncs:
//
//	current.json.manifestDigest == sha256(releases/<id>/manifest.json)
//	manifest.files[name].digest   == sha256(releases/<id>/<name>)
//	manifest.files[name].size     == bytes on disk
//
// It exists because the tree we serve is cached immutably at /v1/releases/* and
// the pointer at /v1/current.json is the only thing that can change. If a
// packaging step ever rewrites, truncates or re-serializes one of those files,
// the origin would keep serving bytes that no digest agrees with, and no client
// would be able to tell us. Verifying before upload turns that into a failed
// deploy instead of a silent corruption.
//
// dir is the directory that CONTAINS v1/ (the deploy bundle root), matching
// `catalog build --out`.
func VerifyMaterializedTree(dir string) (*CurrentPointer, *ReleaseManifest, error) {
	pointerPath := filepath.Join(dir, "v1", "current.json")
	pointerBytes, err := os.ReadFile(pointerPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read pointer %s: %w", pointerPath, err)
	}

	var pointer CurrentPointer
	if err := json.Unmarshal(pointerBytes, &pointer); err != nil {
		return nil, nil, fmt.Errorf("parse pointer %s: %w", pointerPath, err)
	}
	if pointer.SchemaVersion != CurrentPointerSchemaVersion {
		return nil, nil, fmt.Errorf("pointer schemaVersion %d, want %d", pointer.SchemaVersion, CurrentPointerSchemaVersion)
	}
	if err := validateReleaseIDForVerify(pointer.ReleaseID); err != nil {
		return nil, nil, err
	}
	if !strings.HasPrefix(pointer.ManifestDigest, "sha256:") {
		return nil, nil, fmt.Errorf("pointer manifestDigest %q is not a sha256 digest", pointer.ManifestDigest)
	}

	releaseDir := filepath.Join(dir, "v1", "releases", pointer.ReleaseID)
	manifestBytes, err := os.ReadFile(filepath.Join(releaseDir, "manifest.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("read manifest: %w", err)
	}
	if got := digestBytes(manifestBytes); got != pointer.ManifestDigest {
		return nil, nil, fmt.Errorf("manifest digest mismatch: pointer says %s, file is %s", pointer.ManifestDigest, got)
	}

	var manifest ReleaseManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, nil, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.ReleaseID != pointer.ReleaseID {
		return nil, nil, fmt.Errorf("manifest releaseId %q does not match pointer %q", manifest.ReleaseID, pointer.ReleaseID)
	}
	if manifest.ItemCount != pointer.ItemCount {
		return nil, nil, fmt.Errorf("manifest itemCount %d does not match pointer %d", manifest.ItemCount, pointer.ItemCount)
	}
	if len(manifest.Files) == 0 {
		return nil, nil, fmt.Errorf("manifest lists no files")
	}

	for name, entry := range manifest.Files {
		if strings.Contains(name, "..") || filepath.IsAbs(name) {
			return nil, nil, fmt.Errorf("manifest declares an unsafe file path %q", name)
		}
		path := filepath.Join(releaseDir, filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", name, err)
		}
		if int64(len(data)) != entry.Size {
			return nil, nil, fmt.Errorf("%s: size %d on disk, manifest says %d", name, len(data), entry.Size)
		}
		if got := digestBytes(data); got != entry.Digest {
			return nil, nil, fmt.Errorf("%s: digest mismatch: manifest says %s, file is %s", name, entry.Digest, got)
		}
	}

	return &pointer, &manifest, nil
}

// validateReleaseIDForVerify keeps the pointer's release id path-safe before it
// is joined onto a directory.
func validateReleaseIDForVerify(id string) error {
	if id == "" {
		return fmt.Errorf("pointer releaseId is empty")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." || strings.HasPrefix(id, ".") {
		return fmt.Errorf("pointer releaseId %q is not a safe path segment", id)
	}
	return nil
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
