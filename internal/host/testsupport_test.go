package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// useTempHome pins every environment variable the path resolvers consult to one
// temporary directory, and returns it.
//
// This exists because the resolvers read the ambient environment, and CI runners
// do not start from a clean slate:
//   - resolveHomeDir prefers USERPROFILE on Windows and HOME elsewhere, so a
//     test that sets only HOME resolves to the real user profile on the Windows
//     runner and writes to that account's actual agent configuration;
//   - xdgConfigDir honours XDG_CONFIG_HOME, which GitHub runners export;
//   - windowsAppData honours %APPDATA% on native Windows.
//
// Without this, these tests would not merely fail -- some of them would edit
// files outside the sandbox.
func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return home
}

// adapterFor resolves a target and skips the test if it is not registered.
func adapterFor(t *testing.T, id string) *GenericAdapter {
	t.Helper()
	tgt, ok := LookupBridgeTarget(id)
	if !ok {
		t.Fatalf("bridge target %q is not registered", id)
	}
	return NewGenericAdapter(tgt)
}

// configPathOf asks the adapter where it will act, rather than assuming. Tests
// that hardcode a path break the moment the resolver is platform-aware.
func configPathOf(t *testing.T, adapter *GenericAdapter) string {
	t.Helper()
	path, err := adapter.DetectConfig(context.Background(), domain.ScopeUser)
	if err != nil {
		t.Fatalf("DetectConfig: %v", err)
	}
	return path
}

// writeConfig creates the parent directory and writes content to path.
func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// configPathOfAny is configPathOf for any registered adapter, including the
// hand-written ones that are not GenericAdapter.
func configPathOfAny(t *testing.T, adapter HostAdapter) string {
	t.Helper()
	path, err := adapter.DetectConfig(context.Background(), domain.ScopeUser)
	if err != nil {
		t.Fatalf("DetectConfig: %v", err)
	}
	return path
}
