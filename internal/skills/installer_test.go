package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillBody(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testInstaller(t *testing.T) (*Installer, *Ledger) {
	t.Helper()
	l, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	return NewInstaller(l, nil), l
}

func TestInstallerInstallPolicyDeny(t *testing.T) {
	in, ledger := testInstaller(t)
	in.Policy = func(ctx context.Context, req PolicyRequest) PolicyVerdict {
		return PolicyVerdict{Allowed: false, Decision: "deny", Reason: "blocked by test"}
	}
	src := t.TempDir()
	writeSkillBody(t, src, "# pdf\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")

	var denied *PolicyDeniedError
	_, err := in.Install(context.Background(), InstallRequest{
		Op:     InstallOp{SkillName: "pdf", FromDir: src, ToDir: dst, HostLabel: "claude-code"},
		Source: SkillSource{Display: "owner/pdf"},
		Scope:  "project",
	})
	if !errors.As(err, &denied) {
		t.Fatalf("expected a PolicyDeniedError, got %v", err)
	}
	if denied.Verdict.Decision != "deny" {
		t.Fatalf("error must carry the decision, got %q", denied.Verdict.Decision)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatal("policy denial must happen before any copy")
	}
	if all, _ := ledger.All(); len(all) != 0 {
		t.Fatalf("nothing should be recorded on denial, got %+v", all)
	}
}

func TestInstallerInstallPolicyAllowRecordsProvenance(t *testing.T) {
	in, ledger := testInstaller(t)
	var seen PolicyRequest
	in.Policy = func(ctx context.Context, req PolicyRequest) PolicyVerdict {
		seen = req
		return PolicyVerdict{Allowed: true, Decision: "allow"}
	}
	src := t.TempDir()
	writeSkillBody(t, src, "# pdf\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")

	entry, err := in.Install(context.Background(), InstallRequest{
		Op:          InstallOp{SkillName: "pdf", FromDir: src, ToDir: dst, HostLabel: "claude-code"},
		Source:      SkillSource{Display: "owner/repo", Pin: "v1.0.0"},
		Scope:       "project",
		ObservedRef: "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Operation != "install" || seen.DestDir != dst || seen.SkillName != "pdf" {
		t.Fatalf("policy request malformed: %+v", seen)
	}
	if len(seen.Effects) == 0 || seen.Effects[0] != "filesystem.write" {
		t.Fatalf("policy request must declare the write effect: %+v", seen.Effects)
	}
	if entry.ContentDigest == "" || len(entry.Inventory) != 1 || entry.SourceRef != "v1.0.0" {
		t.Fatalf("provenance not recorded: %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Fatalf("skill not installed: %v", err)
	}
	all, _ := ledger.All()
	if len(all) != 1 || all[0].ContentDigest != entry.ContentDigest {
		t.Fatalf("ledger not written: %+v", all)
	}
}

func TestInstallerUpdateHappyPath(t *testing.T) {
	in, ledger := testInstaller(t)
	v1 := t.TempDir()
	writeSkillBody(t, v1, "# v1\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")
	entry, err := in.Install(context.Background(), InstallRequest{
		Op:     InstallOp{SkillName: "pdf", FromDir: v1, ToDir: dst, HostLabel: "claude-code"},
		Source: SkillSource{Display: "owner/repo"},
		Scope:  "project",
	})
	if err != nil {
		t.Fatal(err)
	}

	v2 := t.TempDir()
	writeSkillBody(t, v2, "# v2\n")
	outcome, err := in.Update(context.Background(), UpdateRequest{
		Entry:     entry,
		NewDir:    v2,
		NewSource: "owner/repo",
		NewRef:    "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Updated || outcome.AlreadyCurrent {
		t.Fatalf("expected an update, got %+v", outcome)
	}
	data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil || string(data) != "# v2\n" {
		t.Fatalf("destination not updated: %q %v", data, err)
	}
	if outcome.Entry.ContentDigest == entry.ContentDigest {
		t.Fatal("digest should change after an update")
	}
	if outcome.Entry.SourceRef != "abc123" {
		t.Fatalf("new ref not recorded: %+v", outcome.Entry)
	}

	reopened, _ := OpenLedger(ledger.path)
	all, _ := reopened.All()
	if len(all) != 1 || all[0].ContentDigest != outcome.Entry.ContentDigest || all[0].SourceRef != "abc123" {
		t.Fatalf("ledger not updated: %+v", all)
	}
	// No temporary swap artefacts should remain.
	assertNoSwapArtefacts(t, filepath.Dir(dst))
}

func TestInstallerUpdateAlreadyCurrent(t *testing.T) {
	in, _ := testInstaller(t)
	src := t.TempDir()
	writeSkillBody(t, src, "# same\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")
	entry, err := in.Install(context.Background(), InstallRequest{
		Op:     InstallOp{SkillName: "pdf", FromDir: src, ToDir: dst, HostLabel: "claude-code"},
		Source: SkillSource{Display: "owner/repo"},
		Scope:  "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(dst, "SKILL.md"))

	outcome, err := in.Update(context.Background(), UpdateRequest{Entry: entry, NewDir: src})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Updated || !outcome.AlreadyCurrent {
		t.Fatalf("identical content must be an honest no-op, got %+v", outcome)
	}
	after, _ := os.Stat(filepath.Join(dst, "SKILL.md"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("no-op update must not touch the existing files")
	}
}

func TestInstallerUpdateFailureLeavesOldIntact(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, newDir string)
	}{
		{
			name:    "missing SKILL.md",
			prepare: func(t *testing.T, newDir string) {},
		},
		{
			name: "symlink in new content",
			prepare: func(t *testing.T, newDir string) {
				writeSkillBody(t, newDir, "# v2\n")
				if err := os.Symlink(t.TempDir(), filepath.Join(newDir, "escape")); err != nil {
					t.Skipf("symlinks unsupported: %v", err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, _ := testInstaller(t)
			v1 := t.TempDir()
			writeSkillBody(t, v1, "# v1\n")
			dst := filepath.Join(t.TempDir(), "skills", "pdf")
			entry, err := in.Install(context.Background(), InstallRequest{
				Op:     InstallOp{SkillName: "pdf", FromDir: v1, ToDir: dst, HostLabel: "claude-code"},
				Source: SkillSource{Display: "owner/repo"},
				Scope:  "project",
			})
			if err != nil {
				t.Fatal(err)
			}
			newDir := t.TempDir()
			c.prepare(t, newDir)

			if _, err := in.Update(context.Background(), UpdateRequest{Entry: entry, NewDir: newDir}); err == nil {
				t.Fatal("expected the update to fail")
			}
			data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
			if err != nil || string(data) != "# v1\n" {
				t.Fatalf("previous install was disturbed: %q %v", data, err)
			}
			assertNoSwapArtefacts(t, filepath.Dir(dst))
		})
	}
}

func TestInstallerUpdatePolicyDenyLeavesOldIntact(t *testing.T) {
	in, _ := testInstaller(t)
	v1 := t.TempDir()
	writeSkillBody(t, v1, "# v1\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")
	entry, err := in.Install(context.Background(), InstallRequest{
		Op:     InstallOp{SkillName: "pdf", FromDir: v1, ToDir: dst, HostLabel: "claude-code"},
		Source: SkillSource{Display: "owner/repo"},
		Scope:  "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	v2 := t.TempDir()
	writeSkillBody(t, v2, "# v2\n")
	in.Policy = func(ctx context.Context, req PolicyRequest) PolicyVerdict {
		if req.Operation != "update" {
			t.Errorf("expected update operation, got %q", req.Operation)
		}
		return PolicyVerdict{Allowed: false, Decision: "ask", Reason: "needs approval"}
	}

	var denied *PolicyDeniedError
	if _, err := in.Update(context.Background(), UpdateRequest{Entry: entry, NewDir: v2}); !errors.As(err, &denied) {
		t.Fatalf("expected PolicyDeniedError, got %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if string(data) != "# v1\n" {
		t.Fatalf("policy denial must not change the install: %q", data)
	}
}

func TestInstallerUpdateLedgerFailureRollsBack(t *testing.T) {
	in, ledger := testInstaller(t)
	v1 := t.TempDir()
	writeSkillBody(t, v1, "# v1\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")
	entry, err := in.Install(context.Background(), InstallRequest{
		Op:     InstallOp{SkillName: "pdf", FromDir: v1, ToDir: dst, HostLabel: "claude-code"},
		Source: SkillSource{Display: "owner/repo"},
		Scope:  "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Sabotage the ledger so ReplaceEntry has nothing to replace.
	if err := ledger.write([]LedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	v2 := t.TempDir()
	writeSkillBody(t, v2, "# v2\n")

	_, err = in.Update(context.Background(), UpdateRequest{Entry: entry, NewDir: v2})
	if err == nil || !strings.Contains(err.Error(), "no ledger entry") {
		t.Fatalf("expected a ledger failure, got %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if readErr != nil || string(data) != "# v1\n" {
		t.Fatalf("rollback did not restore the previous install: %q %v", data, readErr)
	}
	assertNoSwapArtefacts(t, filepath.Dir(dst))
}

func TestInstallerUpdateDryRunChangesNothing(t *testing.T) {
	in, ledger := testInstaller(t)
	v1 := t.TempDir()
	writeSkillBody(t, v1, "# v1\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")
	entry, err := in.Install(context.Background(), InstallRequest{
		Op:     InstallOp{SkillName: "pdf", FromDir: v1, ToDir: dst, HostLabel: "claude-code"},
		Source: SkillSource{Display: "owner/repo"},
		Scope:  "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	v2 := t.TempDir()
	writeSkillBody(t, v2, "# v2\n")

	outcome, err := in.Update(context.Background(), UpdateRequest{Entry: entry, NewDir: v2, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.WouldUpdate || outcome.Updated || outcome.AlreadyCurrent {
		t.Fatalf("dry run should report a pending update, got %+v", outcome)
	}
	data, _ := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if string(data) != "# v1\n" {
		t.Fatalf("dry run modified the install: %q", data)
	}
	all, _ := ledger.All()
	if len(all) != 1 || all[0].ContentDigest != entry.ContentDigest {
		t.Fatalf("dry run modified the ledger: %+v", all)
	}
	assertNoSwapArtefacts(t, filepath.Dir(dst))
}

func TestInstallerUpdateRefusesUserAddedFileUnlessForced(t *testing.T) {
	in, _ := testInstaller(t)
	v1 := t.TempDir()
	writeSkillBody(t, v1, "# v1\n")
	dst := filepath.Join(t.TempDir(), "skills", "pdf")
	entry, err := in.Install(context.Background(), InstallRequest{
		Op:     InstallOp{SkillName: "pdf", FromDir: v1, ToDir: dst, HostLabel: "claude-code"},
		Source: SkillSource{Display: "owner/repo"},
		Scope:  "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(dst, "my-notes.txt")
	if err := os.WriteFile(userFile, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	v2 := t.TempDir()
	writeSkillBody(t, v2, "# v2\n")

	if _, err := in.Update(context.Background(), UpdateRequest{Entry: entry, NewDir: v2}); err == nil {
		t.Fatal("update must refuse to destroy a user-added file")
	}
	data, _ := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if string(data) != "# v1\n" {
		t.Fatalf("refused update must not touch the install: %q", data)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatal("user file was destroyed by a refused update")
	}

	outcome, err := in.Update(context.Background(), UpdateRequest{Entry: entry, NewDir: v2, Force: true})
	if err != nil {
		t.Fatalf("forced update failed: %v", err)
	}
	if !outcome.Updated {
		t.Fatalf("forced update should proceed, got %+v", outcome)
	}
	data, _ = os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if string(data) != "# v2\n" {
		t.Fatalf("destination not updated after force: %q", data)
	}
}

func TestInstallerUpdateRefusesSymlinkedDestination(t *testing.T) {
	in, _ := testInstaller(t)
	real := t.TempDir()
	writeSkillBody(t, real, "# v1\n")
	link := filepath.Join(t.TempDir(), "skills", "linked")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	newDir := t.TempDir()
	writeSkillBody(t, newDir, "# v2\n")

	if _, err := in.Update(context.Background(), UpdateRequest{Entry: LedgerEntry{DestDir: link, Scope: "project"}, NewDir: newDir}); err == nil {
		t.Fatal("a symlinked destination must be refused")
	}
	if _, err := os.Stat(filepath.Join(real, "SKILL.md")); err != nil {
		t.Fatal("symlink target must be left alone")
	}
}

// assertNoSwapArtefacts fails when a swap temp or backup directory is left
// behind next to the installed skill.
func assertNoSwapArtefacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".litespm-new-") || strings.Contains(e.Name(), ".litespm-old-") {
			t.Fatalf("swap artefact left behind: %s", e.Name())
		}
	}
}
