package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestEntryFingerprintIsHashOfWrittenEntryAndDetectsEdits(t *testing.T) {
	for _, hostID := range []string{"claude-code", "codex", "opencode"} {
		t.Run(hostID, func(t *testing.T) {
			entryHome(t)
			ctx := context.Background()
			backups := filepath.Join(t.TempDir(), "b")
			res, err := InstallServerEntry(ctx, hostID, ServerEntry{Name: "demo", Command: "npx", Args: []string{"-y", "demo"}},
				EntryInstallOptions{BackupDir: backups, Scope: domain.ScopeUser})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(res.Fingerprint, "sha256:") || len(res.Fingerprint) != len("sha256:")+64 {
				t.Fatalf("fingerprint is not a sha256: %q", res.Fingerprint)
			}
			st, err := ReadServerEntryState(ctx, hostID, "demo", domain.ScopeUser)
			if err != nil || st == nil {
				t.Fatalf("read state: %v %v", st, err)
			}
			if st.Fingerprint != res.Fingerprint {
				t.Errorf("read-back fingerprint %s != install fingerprint %s", st.Fingerprint, res.Fingerprint)
			}
			// Another install changes siblings, not our node's fingerprint.
			if _, err := InstallServerEntry(ctx, hostID, ServerEntry{Name: "other", Command: "x"},
				EntryInstallOptions{BackupDir: backups, Scope: domain.ScopeUser}); err != nil {
				t.Fatal(err)
			}
			st2, _ := ReadServerEntryState(ctx, hostID, "demo", domain.ScopeUser)
			if st2 == nil || st2.Fingerprint != res.Fingerprint {
				t.Errorf("sibling install altered the fingerprint of an untouched node")
			}
			// Distinct entries have distinct fingerprints.
			o, _ := ReadServerEntryState(ctx, hostID, "other", domain.ScopeUser)
			if o == nil || o.Fingerprint == res.Fingerprint {
				t.Errorf("distinct entries must not share a fingerprint")
			}
		})
	}
}

func TestRemoveServerEntryRemovesOnlyTheOwnedEntry(t *testing.T) {
	home := entryHome(t)
	ctx := context.Background()
	file := filepath.Join(home, ".claude.json")
	original := "{\n  // keep me\n  \"theme\": \"dark\",\n  \"mcpServers\": { \"mine\": { \"command\": \"npx\" } }\n}\n"
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(home, "backups")
	if _, err := InstallServerEntry(ctx, "claude-code", ServerEntry{Name: "demo", Command: "npx"},
		EntryInstallOptions{BackupDir: backups, Scope: domain.ScopeUser}); err != nil {
		t.Fatal(err)
	}
	res, err := RemoveServerEntry(ctx, "claude-code", "demo", domain.ScopeUser, backups, "")
	if err != nil || !res.Removed {
		t.Fatalf("remove: %+v %v", res, err)
	}
	after, _ := os.ReadFile(file)
	if strings.Contains(string(after), "demo") {
		t.Errorf("owned entry still present:\n%s", after)
	}
	for _, want := range []string{"// keep me", `"theme": "dark"`, `"mine"`} {
		if !strings.Contains(string(after), want) {
			t.Errorf("removal destroyed %q:\n%s", want, after)
		}
	}
	// Second removal reports absence instead of failing or rewriting.
	res, err = RemoveServerEntry(ctx, "claude-code", "demo", domain.ScopeUser, backups, "")
	if err != nil || res.Removed {
		t.Errorf("repeat remove should report not-present: %+v %v", res, err)
	}
}

func TestForceReplaceThenRemoveRestoresPriorEntry(t *testing.T) {
	for _, hostID := range []string{"claude-code", "codex"} {
		t.Run(hostID, func(t *testing.T) {
			home := entryHome(t)
			ctx := context.Background()
			backups := filepath.Join(home, "b")
			// The user's own `demo` entry already exists.
			if _, err := InstallServerEntry(ctx, hostID, ServerEntry{Name: "demo", Command: "user-cmd", Args: []string{"--mine"}},
				EntryInstallOptions{BackupDir: backups, Scope: domain.ScopeUser}); err != nil {
				t.Fatal(err)
			}
			prior, _ := ReadServerEntryState(ctx, hostID, "demo", domain.ScopeUser)
			res, err := InstallServerEntry(ctx, hostID, ServerEntry{Name: "demo", Command: "litespm-cmd"},
				EntryInstallOptions{BackupDir: backups, Scope: domain.ScopeUser, Force: true})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Replaced || res.PriorEntry == "" || res.PriorFingerprint != prior.Fingerprint {
				t.Fatalf("replace must capture the prior entry: %+v (prior fp %s)", res, prior.Fingerprint)
			}
			rm, err := RemoveServerEntry(ctx, hostID, "demo", domain.ScopeUser, backups, res.PriorEntry)
			if err != nil || !rm.Restored {
				t.Fatalf("restore: %+v %v", rm, err)
			}
			now, _ := ReadServerEntryState(ctx, hostID, "demo", domain.ScopeUser)
			if now == nil || now.Fingerprint != prior.Fingerprint {
				t.Errorf("prior entry not restored byte-for-byte semantically: %+v vs %+v", now, prior)
			}
		})
	}
}
