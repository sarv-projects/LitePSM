package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// skipUnlessUnix hides the runtime-root ownership model on platforms where it
// does not apply: unix uids and symlinks are the threat being tested, and
// Windows resolves both differently (ACLs, privileged links).
func skipUnlessUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runtime-root ownership and symlink semantics differ on Windows")
	}
}

func mode0700(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if got := fi.Mode().Perm(); got != 0700 {
		t.Fatalf("runtime root %s mode = %04o, want 0700", dir, got)
	}
}

func TestEnsureRuntimeRootCreatesPrivateDir(t *testing.T) {
	skipUnlessUnix(t)

	root := filepath.Join(t.TempDir(), "run", "litespm")
	if err := ensureRuntimeRoot(root); err != nil {
		t.Fatalf("ensureRuntimeRoot: %v", err)
	}
	fi, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory, got mode %v", fi.Mode())
	}
	mode0700(t, root)
}

func TestEnsureRuntimeRootTightensLoosenedOwnedDir(t *testing.T) {
	skipUnlessUnix(t)

	root := filepath.Join(t.TempDir(), "litespm-uid")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := ensureRuntimeRoot(root); err != nil {
		t.Fatalf("ensureRuntimeRoot on an owned 0755 dir: %v", err)
	}
	mode0700(t, root)
}

func TestEnsureRuntimeRootRefusesSymlink(t *testing.T) {
	skipUnlessUnix(t)

	base := t.TempDir()
	target := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(base, "litespm-uid")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := ensureRuntimeRoot(link)
	if !errors.Is(err, ErrUnsafeRuntimeRoot) {
		t.Fatalf("expected ErrUnsafeRuntimeRoot, got %v", err)
	}
	var typed *UnsafeRuntimeRootError
	if !errors.As(err, &typed) {
		t.Fatalf("expected *UnsafeRuntimeRootError, got %T", err)
	}
	if typed.Reason != RuntimeRootReasonSymlink {
		t.Fatalf("reason = %q, want %q", typed.Reason, RuntimeRootReasonSymlink)
	}
	// The link must be left exactly as found: refusing is the whole point.
	if fi, lerr := os.Lstat(link); lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was modified or removed (fi=%v, err=%v)", fi, lerr)
	}
}

func TestEnsureRuntimeRootRefusesNonDirectory(t *testing.T) {
	skipUnlessUnix(t)

	path := filepath.Join(t.TempDir(), "litespm-uid")
	if err := os.WriteFile(path, []byte("not a dir"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := ensureRuntimeRoot(path)
	if !errors.Is(err, ErrUnsafeRuntimeRoot) {
		t.Fatalf("expected ErrUnsafeRuntimeRoot, got %v", err)
	}
	var typed *UnsafeRuntimeRootError
	if !errors.As(err, &typed) {
		t.Fatalf("expected *UnsafeRuntimeRootError, got %T", err)
	}
	if typed.Reason != RuntimeRootReasonNotDirectory {
		t.Fatalf("reason = %q, want %q", typed.Reason, RuntimeRootReasonNotDirectory)
	}
}

func TestEnsureRuntimeRootRefusesForeignOwner(t *testing.T) {
	skipUnlessUnix(t)
	if os.Geteuid() != 0 {
		t.Skip("chown to another uid requires root")
	}

	root := filepath.Join(t.TempDir(), "litespm-uid")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Running as root: hand the directory to an unprivileged uid.
	if err := os.Chown(root, 65534, 65534); err != nil {
		t.Fatalf("chown: %v", err)
	}

	err := ensureRuntimeRoot(root)
	if !errors.Is(err, ErrUnsafeRuntimeRoot) {
		t.Fatalf("expected ErrUnsafeRuntimeRoot, got %v", err)
	}
	var typed *UnsafeRuntimeRootError
	if !errors.As(err, &typed) {
		t.Fatalf("expected *UnsafeRuntimeRootError, got %T", err)
	}
	if typed.Reason != RuntimeRootReasonForeignOwner &&
		!strings.HasPrefix(typed.Reason, RuntimeRootReasonForeignOwner) {
		t.Fatalf("reason = %q, want prefix %q", typed.Reason, RuntimeRootReasonForeignOwner)
	}
}

// The public entry point must surface the same fail-closed error: a daemon
// that calls EnsureDirectories and ignores ErrUnsafeRuntimeRoot would still
// bind into the planted directory, so the typed error has to reach it.
func TestEnsureDirectoriesFailsClosedOnPlantedRuntimeRoot(t *testing.T) {
	skipUnlessUnix(t)

	base := t.TempDir()
	target := filepath.Join(base, "attacker")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	paths := &PlatformPaths{
		DataRoot:    filepath.Join(base, "data"),
		ConfigRoot:  filepath.Join(base, "config"),
		RuntimeRoot: filepath.Join(base, "litespm-uid"),
	}
	if err := os.Symlink(target, paths.RuntimeRoot); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := paths.EnsureDirectories()
	if !errors.Is(err, ErrUnsafeRuntimeRoot) {
		t.Fatalf("EnsureDirectories: expected ErrUnsafeRuntimeRoot, got %v", err)
	}
	// Nothing may have been created under the attacker-controlled target.
	if entries, rerr := os.ReadDir(target); rerr != nil || len(entries) != 0 {
		t.Fatalf("attacker dir was written into: entries=%v err=%v", entries, rerr)
	}
	// Data/config roots are not the socket directory, so the rest of the
	// layout must still have been provisioned (the error is specific).
	if _, derr := os.Stat(paths.DataRoot); derr != nil {
		t.Fatalf("data root should still be created: %v", derr)
	}
}

func TestEnsureRuntimeRootMissingParentIsCreated(t *testing.T) {
	skipUnlessUnix(t)

	root := filepath.Join(t.TempDir(), "a", "b", "litespm")
	if err := ensureRuntimeRoot(root); err != nil {
		t.Fatalf("ensureRuntimeRoot with missing parents: %v", err)
	}
	mode0700(t, root)
}
