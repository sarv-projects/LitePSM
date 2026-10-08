package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func entryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return home
}

// TestInstallServerEntryPreservesTheRestOfTheConfig is the property that matters
// most for an installer: it adds ONE member and leaves the user's file otherwise
// untouched, byte for byte.
func TestInstallServerEntryPreservesTheRestOfTheConfig(t *testing.T) {
	home := entryHome(t)
	ctx := context.Background()

	claudeFile := filepath.Join(home, ".claude.json")
	original := "{\n  // my settings\n  \"theme\": \"One Dark\",\n" +
		"  \"mcpServers\": { \"mine\": { \"command\": \"npx\", \"args\": [\"-y\", \"mine\"] } }\n}\n"
	if err := os.WriteFile(claudeFile, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := InstallServerEntry(ctx, "claude-code", ServerEntry{
		Name: "brave-search", Command: "npx", Args: []string{"-y", "brave-search-mcp-server"},
	}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser})
	if err != nil {
		t.Fatalf("InstallServerEntry: %v", err)
	}
	if result.HostID != "claude-code" || result.Replaced || result.Created {
		t.Errorf("unexpected result: %+v", result)
	}

	after, err := os.ReadFile(claudeFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(after)
	for _, want := range []string{"// my settings", `"theme": "One Dark"`, `"mine": { "command": "npx", "args": ["-y", "mine"] }`} {
		if !strings.Contains(got, want) {
			t.Errorf("install destroyed %q:\n%s", want, got)
		}
	}
	if strings.Count(got, `"command"`) != 2 {
		t.Errorf("expected exactly one new command entry:\n%s", got)
	}
	if !strings.HasSuffix(got, "}\n") {
		t.Errorf("trailing newline lost:\n%q", got)
	}

	// A backup of the pre-edit file must exist, since this is a user file.
	entries, err := os.ReadDir(filepath.Join(home, "backups"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no backup was written (err=%v)", err)
	}
}

// TestInstallServerEntryRefusesCollisions stops an install from silently
// reconfiguring a server the user set up by hand.
func TestInstallServerEntryRefusesCollisions(t *testing.T) {
	home := entryHome(t)
	ctx := context.Background()
	claudeFile := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"mine":{"command":"/usr/local/bin/my-own-server"}}}`
	if err := os.WriteFile(claudeFile, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	entry := ServerEntry{Name: "mine", Command: "npx", Args: []string{"-y", "other"}}
	if _, err := InstallServerEntry(ctx, "claude-code", entry, EntryInstallOptions{
		BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser,
	}); err == nil {
		t.Fatal("install overwrote an existing entry without --force")
	}

	after, _ := os.ReadFile(claudeFile)
	if string(after) != original {
		t.Errorf("a refused install must not modify the file:\n%s", after)
	}

	// With force it replaces, and says so.
	result, err := InstallServerEntry(ctx, "claude-code", entry, EntryInstallOptions{
		BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser, Force: true,
	})
	if err != nil {
		t.Fatalf("forced install: %v", err)
	}
	if !result.Replaced {
		t.Errorf("forced install did not report a replacement: %+v", result)
	}
	after, _ = os.ReadFile(claudeFile)
	if !strings.Contains(string(after), "-y") || strings.Contains(string(after), "my-own-server") {
		t.Errorf("forced install did not replace the entry:\n%s", after)
	}
}

func TestAtomicWriteFileIfUnchangedRejectsConcurrentEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFileIfUnchanged(path, []byte("merged"), 0o600, []byte("original"), true); err == nil {
		t.Fatal("stale config snapshot was written over a concurrent edit")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "user edit" {
		t.Fatalf("concurrent edit was overwritten: %q", after)
	}
}

func TestAtomicWriteFileIfUnchangedRefusesUnexpectedCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("created concurrently"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFileIfUnchanged(path, []byte("replacement"), 0o600, nil, false); err == nil {
		t.Fatal("unexpectedly replaced a file created after the absent snapshot")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "created concurrently" {
		t.Fatalf("concurrent create was overwritten: %q", after)
	}
}

func TestConcurrentInstallServerEntriesPreserveBothMerges(t *testing.T) {
	home := entryHome(t)
	configPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"mine":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"alpha", "beta"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := InstallServerEntry(context.Background(), "claude-code", ServerEntry{
				Name: name, Command: "npx", Args: []string{"-y", name},
			}, EntryInstallOptions{Scope: domain.ScopeUser, BackupDir: t.TempDir()})
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent entry install failed: %v", err)
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{`"alpha"`, `"beta"`, `"mine"`} {
		if !strings.Contains(string(data), name) {
			t.Errorf("merged config lost %s: %s", name, data)
		}
	}
}

func TestRollbackConfigWriteDoesNotOverwriteEditsAfterInstall(t *testing.T) {
	for _, tc := range []struct {
		name    string
		created bool
		changed bool
	}{
		{name: "restore unchanged existing config"},
		{name: "preserve edited existing config", changed: true},
		{name: "remove unchanged created config", created: true},
		{name: "preserve edited created config", created: true, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := entryHome(t)
			configPath := filepath.Join(home, ".claude.json")
			original := []byte(`{"theme":"dark","mcpServers":{"mine":{"command":"npx"}}}`)
			if !tc.created {
				if err := os.WriteFile(configPath, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := InstallServerEntry(context.Background(), "claude-code", ServerEntry{
				Name: "demo", Command: "npx", Args: []string{"-y", "demo"},
			}, EntryInstallOptions{Scope: domain.ScopeUser, BackupDir: filepath.Join(home, "backups")})
			if err != nil {
				t.Fatalf("InstallServerEntry: %v", err)
			}

			var edited []byte
			if tc.changed {
				written, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				edited = append(written, []byte("\n// concurrent user edit\n")...)
				if err := os.WriteFile(configPath, edited, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			err = RollbackConfigWrite(result.ConfigPath, result.BackupPath, result.WrittenDigest)
			if tc.changed {
				if err == nil {
					t.Fatal("rollback unexpectedly overwrote a later config edit")
				}
				if got, readErr := os.ReadFile(configPath); readErr != nil || string(got) != string(edited) {
					t.Fatalf("later edit was not preserved: got %q, read error %v", got, readErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("rollback unchanged config: %v", err)
			}
			if tc.created {
				if _, err := os.Stat(configPath); !os.IsNotExist(err) {
					t.Fatalf("created config still exists after rollback: %v", err)
				}
			} else if got, err := os.ReadFile(configPath); err != nil || string(got) != string(original) {
				t.Fatalf("pre-install config was not restored: got %q, err %v", got, err)
			}
		})
	}
}

func TestInstallServerEntryRejectsBadNames(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"", "litespm", "has space", "quote\"name", "dot.name", "../escape"} {
		if err := ValidateServerEntryName(name); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
	for _, name := range []string{"brave-search", "context7", "my_server", "srv2"} {
		if err := ValidateServerEntryName(name); err != nil {
			t.Errorf("name %q was rejected: %v", name, err)
		}
	}
	_ = ctx
}

// TestInstallServerEntryHostShapes walks the shape variants the audit confirmed:
// a plain object host, OpenCode's required `type` + argv array, and a TOML host.
func TestInstallServerEntryHostShapes(t *testing.T) {
	t.Run("opencode_requires_type_local_and_argv", func(t *testing.T) {
		home := entryHome(t)
		ctx := context.Background()
		dir := filepath.Join(home, ".config", "opencode")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := InstallServerEntry(ctx, "opencode", ServerEntry{
			Name: "brave-search", Command: "npx", Args: []string{"-y", "brave-search-mcp-server"},
		}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser}); err != nil {
			t.Fatalf("InstallServerEntry: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
		if err != nil {
			t.Fatal(err)
		}
		root, err := parseHostJSON(data)
		if err != nil {
			t.Fatalf("written config does not parse: %v\n%s", err, data)
		}
		entry, ok := root["mcp"].(map[string]any)["brave-search"].(map[string]any)
		if !ok {
			t.Fatalf("entry missing:\n%s", data)
		}
		if entry["type"] != "local" {
			t.Errorf("opencode requires type=local: %v", entry)
		}
		cmd, ok := entry["command"].([]any)
		if !ok || len(cmd) != 3 || cmd[0] != "npx" {
			t.Errorf("opencode requires a combined argv array: %v", entry["command"])
		}
	})

	t.Run("codex_writes_a_toml_table", func(t *testing.T) {
		home := entryHome(t)
		ctx := context.Background()
		dir := filepath.Join(home, ".codex")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		original := "# my codex settings\nmodel = \"gpt-5\"\n\n[mcp_servers.mine]\ncommand = \"npx\"\n"
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := InstallServerEntry(ctx, "codex", ServerEntry{
			Name: "brave-search", Command: "npx", Args: []string{"-y", "brave-search-mcp-server"},
		}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser}); err != nil {
			t.Fatalf("InstallServerEntry: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		got := string(data)
		if !strings.Contains(got, "[mcp_servers.brave-search]") {
			t.Errorf("table missing:\n%s", got)
		}
		if !strings.Contains(got, `command = "npx"`) || !strings.Contains(got, `args = ["-y", "brave-search-mcp-server"]`) {
			t.Errorf("entry fields wrong:\n%s", got)
		}
		if !strings.Contains(got, "# my codex settings") || !strings.Contains(got, "[mcp_servers.mine]") {
			t.Errorf("user content destroyed:\n%s", got)
		}
	})
}

// TestListServerEntriesNeverReportsTheBridge keeps the collision check from
// firing on LiteSPM's own entry.
func TestListServerEntriesNeverReportsTheBridge(t *testing.T) {
	home := entryHome(t)
	ctx := context.Background()
	claudeFile := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claudeFile, []byte(`{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm"},"context7":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := ListServerEntries(ctx, "claude-code", domain.ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "context7" {
		t.Errorf("expected only the foreign entry, got %v", names)
	}
}

// TestRegisteredBridgeHostsOnlyReportsConfiguredHosts is the consent boundary:
// a host the user never set LiteSPM up in is never a default install target.
func TestRegisteredBridgeHostsOnlyReportsConfiguredHosts(t *testing.T) {
	home := entryHome(t)
	ctx := context.Background()
	if hosts := RegisteredBridgeHosts(ctx, domain.ScopeUser); len(hosts) != 0 {
		t.Errorf("a machine with no setup must report no hosts, got %v", hosts)
	}

	claudeFile := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claudeFile, []byte(`{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts := RegisteredBridgeHosts(ctx, domain.ScopeUser)
	if len(hosts) != 1 || hosts[0] != "claude-code" {
		t.Errorf("expected only the configured host, got %v", hosts)
	}
}
