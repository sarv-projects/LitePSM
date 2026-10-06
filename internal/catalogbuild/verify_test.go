package catalogbuild

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// writeTree materializes a compiled release into dir/v1 the way the deploy
// bundle does, so the verifier is exercised against real files.
func writeTree(t *testing.T, dir string, out *BuildOutput) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "v1", "releases", out.ReleaseID), 0o755); err != nil {
		t.Fatal(err)
	}
	// Files keys are already repo-relative below the bundle root ("v1/...").
	for name, data := range out.Files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "v1", "current.json")
}

func compileFixture(t *testing.T) *BuildOutput {
	t.Helper()
	created := time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)
	out, err := CompileRelease(
		"rel-2026-10-06-01", 7, []string{"dataset:abc"},
		[]*domain.Listing{{ID: "skill:acme:one", Name: "One", Kind: domain.KindSkill}},
		[]*domain.VersionRecord{{ListingID: "skill:acme:one", Version: "1.0.0"}},
		created,
	)
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	return out
}

func TestVerifyMaterializedTreeAcceptsAnUntouchedTree(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, compileFixture(t))

	pointer, manifest, err := VerifyMaterializedTree(dir)
	if err != nil {
		t.Fatalf("VerifyMaterializedTree: %v", err)
	}
	if pointer.ReleaseID != "rel-2026-10-06-01" || pointer.Sequence != 7 {
		t.Errorf("unexpected pointer: %+v", pointer)
	}
	if manifest.ItemCount != 1 {
		t.Errorf("unexpected item count: %d", manifest.ItemCount)
	}
	if len(manifest.Files) < 2 {
		t.Errorf("expected manifest to describe listings and versions, got %v", manifest.Files)
	}
}

// TestVerifyMaterializedTreeRejectsTampering is the reason this exists: the
// release tree is immutable at the CDN, so any disagreement must be a failed
// deploy rather than bytes nobody can account for.
func TestVerifyMaterializedTreeRejectsTampering(t *testing.T) {
	cases := []struct {
		name    string
		tamper  func(t *testing.T, dir string, out *BuildOutput)
		wantSub string
	}{
		{
			name: "listing bytes rewritten",
			tamper: func(t *testing.T, dir string, out *BuildOutput) {
				path := filepath.Join(dir, "v1", "releases", out.ReleaseID, "listings.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, ' '), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "size",
		},
		{
			name: "manifest digest no longer matches the pointer",
			tamper: func(t *testing.T, dir string, out *BuildOutput) {
				path := filepath.Join(dir, "v1", "releases", out.ReleaseID, "manifest.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				// Same byte length, different content: only the digest can catch it.
				replaced := []byte(data)
				replaced[len(replaced)-3] = 'x'
				if err := os.WriteFile(path, replaced, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "manifest digest mismatch",
		},
		{
			name: "a declared file is missing",
			tamper: func(t *testing.T, dir string, out *BuildOutput) {
				if err := os.Remove(filepath.Join(dir, "v1", "releases", out.ReleaseID, "versions.json")); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "read versions.json",
		},
		{
			name: "pointer points at another release",
			tamper: func(t *testing.T, dir string, out *BuildOutput) {
				path := filepath.Join(dir, "v1", "current.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(replaceFirst(string(data), out.ReleaseID, "rel-1999-01-01-01")), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "read manifest",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			out := compileFixture(t)
			writeTree(t, dir, out)
			tc.tamper(t, dir, out)

			if _, _, err := VerifyMaterializedTree(dir); err == nil {
				t.Fatal("verification accepted a tree that does not match its pointer")
			} else if !contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestVerifyMaterializedTreeRejectsUnsafePointer(t *testing.T) {
	dir := t.TempDir()
	out := compileFixture(t)
	writeTree(t, dir, out)

	pointerPath := filepath.Join(dir, "v1", "current.json")
	if err := os.WriteFile(pointerPath, []byte(`{"schemaVersion":1,"releaseId":"../../etc","manifestDigest":"sha256:00","itemCount":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := VerifyMaterializedTree(dir)
	if err == nil || !contains(err.Error(), "safe path segment") {
		t.Fatalf("a traversing release id must be refused, got %v", err)
	}
}

func replaceFirst(s, old, new string) string {
	idx := indexOf(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func contains(haystack, needle string) bool { return indexOf(haystack, needle) >= 0 }
