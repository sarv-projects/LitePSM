package main

// install_txn_test.go — acceptance for the install-side transaction (ARCH/33
// §5, register item A1) and the symmetric removal it feeds (A5).
//
// The property under test is exact: an install either commits completely
// (config writes + install/component/registration/ledger rows) or it fails
// with the machine byte-identical to its pre-install state — no edited config
// left behind, no orphan rows. Failure is injected by dropping the table a
// late state write needs, which exercises the real error path rather than a
// test-only hook.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// mutationsOf reads the deployment-ledger rows recorded for an install.
func mutationsOf(t *testing.T, db *state.DB, installID string) []deployment.Mutation {
	t.Helper()
	muts, err := deployment.NewLedger(db.Raw()).MutationsForInstall(context.Background(), installID)
	if err != nil {
		t.Fatalf("read deployment ledger: %v", err)
	}
	return muts
}

// dropLedgerTable removes the table the last state write of an MCP install
// needs, so the write fails after the config is already on disk.
func dropLedgerTable(t *testing.T, db *state.DB) {
	t.Helper()
	if _, err := db.Raw().ExecContext(context.Background(), `DROP TABLE deployment_mutations;`); err != nil {
		t.Fatalf("inject state failure: %v", err)
	}
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// A1: a state failure after every config write leaves the pre-existing config
// byte-identical and no state row of any kind.
func TestInstallMCPStateFailureRestoresConfigBytes(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	_, configPath := homeWithBridge(t)
	before := fileBytes(t, configPath)

	dropLedgerTable(t, db)

	_, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
	if err == nil {
		t.Fatal("expected the injected state failure to fail the install")
	}

	if after := fileBytes(t, configPath); string(after) != string(before) {
		t.Fatalf("config not byte-identical after rollback:\nbefore: %s\nafter:  %s", before, after)
	}
	assertNoInstallState(t, db, "mcp:example:demo-mcp")
}

// A1: a config the install itself created is removed again on failure — the
// pre-install state was "no file", so rollback means no file.
func TestInstallMCPStateFailureRemovesCreatedConfig(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
	t.Setenv("CLINE_DATA_DIR", "")
	configPath := filepath.Join(home, ".claude.json")
	if _, err := os.Stat(configPath); err == nil {
		t.Fatalf("fixture error: %s already exists", configPath)
	}

	dropLedgerTable(t, db)

	_, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err == nil {
		t.Fatal("expected the injected state failure to fail the install")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("created config %s left behind after rollback: %v", configPath, err)
	}
	assertNoInstallState(t, db, "mcp:example:demo-mcp")
}

// A1 across two hosts: both writes are compensated in reverse order, so a
// state failure leaves one config restored and the other never created.
func TestInstallMCPTwoHostStateFailureRestoresBoth(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
	t.Setenv("CLINE_DATA_DIR", "")

	// Pre-existing claude config (must be restored) and absent cline config
	// (must be removed).
	claudePath := filepath.Join(home, ".claude.json")
	claudeBefore := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"}}}`
	if err := os.WriteFile(claudePath, []byte(claudeBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	clinePath := filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json")

	dropLedgerTable(t, db)

	_, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code", "cline"}, false, stdioRuntime(), nil)
	if err == nil {
		t.Fatal("expected the injected state failure to fail the install")
	}

	if got := string(fileBytes(t, claudePath)); got != claudeBefore {
		t.Errorf("claude config not restored:\n%s", got)
	}
	if _, err := os.Stat(clinePath); !os.IsNotExist(err) {
		t.Errorf("created cline config left behind after rollback: %v", err)
	}
	assertNoInstallState(t, db, "mcp:example:demo-mcp")
}

// assertNoInstallState proves no orphan rows survive: no install, no
// components, no registrations for the entry name.
func assertNoInstallState(t *testing.T, db *state.DB, listingID string) {
	t.Helper()
	ctx := context.Background()
	recs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
	if err != nil {
		t.Fatalf("ListInstalls: %v", err)
	}
	for _, r := range recs {
		if r.ListingID == listingID {
			t.Errorf("orphan install row survived rollback: %+v", r)
		}
	}
	regs, err := db.ListHostRegistrationsForEntry(ctx, domain.ScopeUser, "demo-mcp")
	if err != nil {
		t.Fatalf("ListHostRegistrationsForEntry: %v", err)
	}
	if len(regs) != 0 {
		t.Errorf("orphan host registrations survived rollback: %+v", regs)
	}
}

// A1 + A5: the ledger row carries node-level images — pre-image is the
// fingerprint of the entry this install replaced (the Reconcile A arm), the
// post-image is the entry as written (B), and neither is a whole-file hash.
func TestInstallMCPRecordsNodeLevelPreImage(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	configPath := filepath.Join(home, ".claude.json")
	userOwn := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"demo-mcp":{"command":"my-own-cmd"}}}`
	if err := os.WriteFile(configPath, []byte(userOwn), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, true, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}

	muts := mutationsOf(t, db, outcome.InstallID)
	if len(muts) != 1 {
		t.Fatalf("expected exactly one ledger row, got %d", len(muts))
	}
	m := muts[0]
	if m.PreImageHash == "" {
		t.Error("pre-image is empty: the Reconcile pre-image arm is dead")
	}
	if m.PreImageHash == m.PostImageHash {
		t.Error("pre-image equals post-image although the install replaced an entry")
	}
	if m.PostImageHash != outcome.Hosts[0].Fingerprint {
		t.Errorf("post-image %q is not the written entry's fingerprint %q", m.PostImageHash, outcome.Hosts[0].Fingerprint)
	}
	if m.PostImageHash == sha256Hex(fileBytes(t, configPath)) {
		t.Error("post-image is the whole-file hash; reconcile must be node-level")
	}
	if !strings.Contains(m.PriorEntry, "my-own-cmd") {
		t.Errorf("prior entry not recorded for restore: %q", m.PriorEntry)
	}
	if m.PreImageHash != outcome.Hosts[0].PriorFingerprint {
		t.Errorf("pre-image %q is not the replaced entry's fingerprint %q", m.PreImageHash, outcome.Hosts[0].PriorFingerprint)
	}
}

// A5: removing a ledgered install strips our node, restores the user's entry
// it replaced, keeps siblings, and deletes every state row — and a second
// removal is an honest not-found rather than a second silent delete.
func TestInstallRemoveRestoresPriorEntrySymmetrically(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	configPath := filepath.Join(home, ".claude.json")
	userOwn := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"},"demo-mcp":{"command":"my-own-cmd"}}}`
	if err := os.WriteFile(configPath, []byte(userOwn), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, true, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}
	if err := removeInstalledPackage(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got := string(fileBytes(t, configPath))
	if !strings.Contains(got, `"my-own-cmd"`) {
		t.Errorf("the user's replaced entry was not restored:\n%s", got)
	}
	if strings.Contains(got, `"-y"`) {
		t.Errorf("LiteSPM's own entry text survived the restore:\n%s", got)
	}
	if !strings.Contains(got, `"litespm"`) || !strings.Contains(got, `"mine"`) {
		t.Errorf("sibling entries were disturbed:\n%s", got)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err == nil {
		t.Error("install row survived removal")
	}
	regs, err := db.ListHostRegistrationsForEntry(context.Background(), domain.ScopeUser, "demo-mcp")
	if err != nil {
		t.Fatalf("ListHostRegistrationsForEntry: %v", err)
	}
	if len(regs) != 0 {
		t.Errorf("host registration survived removal: %+v", regs)
	}
	if muts := mutationsOf(t, db, outcome.InstallID); len(muts) != 0 {
		t.Errorf("deployment rows survived removal: %+v", muts)
	}

	// Idempotent honesty: removing again reports not-found, not success.
	err = removeInstalledPackage(context.Background(), db, dataRoot, outcome.InstallID)
	if err == nil || domain.ErrorCode(err) != "LPSM-STATE-NOT-FOUND" {
		t.Errorf("second remove: err = %v, want LPSM-STATE-NOT-FOUND", err)
	}
}

// A5: a node the user edited after install is theirs now — removal keeps it
// (detach), never strips what no longer matches what LiteSPM wrote, and still
// deletes the install state.
func TestInstallRemoveKeepsUserEditedNode(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	configPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// The user rewrites our entry to something of their own.
	edited := map[string]any{}
	if err := json.Unmarshal(fileBytes(t, configPath), &edited); err != nil {
		t.Fatal(err)
	}
	servers := edited["mcpServers"].(map[string]any)
	servers["demo-mcp"] = map[string]any{"command": "user-override"}
	editedBytes, _ := json.Marshal(edited)
	if err := os.WriteFile(configPath, editedBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := removeInstalledPackage(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := string(fileBytes(t, configPath))
	if !strings.Contains(got, "user-override") {
		t.Errorf("user-edited node was stripped; it must be kept:\n%s", got)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err == nil {
		t.Error("install row survived removal")
	}
}

// A5 pre-ledger fallback: with no deployment rows (an install that predates
// the ledger), removal still strips the entry for real, driven by the host
// registrations the install recorded.
func TestInstallRemovePreLedgerStripsViaRegistrations(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	configPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, err := installMCPFromListing(context.Background(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// Simulate a pre-ledger install: state rows exist, ledger rows do not.
	if _, err := db.Raw().ExecContext(context.Background(),
		`DELETE FROM deployment_mutations WHERE install_id = ?;`, outcome.InstallID); err != nil {
		t.Fatalf("strip ledger rows: %v", err)
	}

	if err := removeInstalledPackage(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := string(fileBytes(t, configPath))
	if strings.Contains(got, "demo-mcp") {
		t.Errorf("entry not stripped on the pre-ledger path:\n%s", got)
	}
	if !strings.Contains(got, `"litespm"`) || !strings.Contains(got, `"mine"`) {
		t.Errorf("sibling entries were disturbed:\n%s", got)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err == nil {
		t.Error("install row survived removal")
	}
	regs, err := db.ListHostRegistrationsForEntry(context.Background(), domain.ScopeUser, "demo-mcp")
	if err != nil {
		t.Fatalf("ListHostRegistrationsForEntry: %v", err)
	}
	if len(regs) != 0 {
		t.Errorf("host registration survived the pre-ledger strip: %+v", regs)
	}
}

// A1 for the skill path: a state failure after the files are copied removes
// the directories and their skills-ledger rows, and no install state remains.
func TestInstallSkillStateFailureRollsBack(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	project := t.TempDir()
	home := t.TempDir()
	agent, destDir := projectAgent(t, project, home)

	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "rollback fixture")
	listing := skillListingFor("skill:example:demo-skill", "demo-skill", skillDir)

	dropLedgerTable(t, db)

	_, err := installSkillFromListing(authorizedTestContext(), db, dataRoot, project, home,
		listing, "1.0.0", domain.ScopeProject, []string{agent})
	if err == nil {
		t.Fatal("expected the injected state failure to fail the install")
	}
	if _, err := os.Stat(filepath.Join(destDir, "demo-skill")); !os.IsNotExist(err) {
		t.Errorf("skill directory left behind after rollback: %v", err)
	}
	ledger, lerr := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if lerr != nil {
		t.Fatalf("open skills ledger: %v", lerr)
	}
	if entries, lerr := ledger.All(); lerr != nil {
		t.Fatalf("read skills ledger: %v", lerr)
	} else if len(entries) != 0 {
		t.Errorf("skills ledger rows left behind after rollback: %+v", entries)
	}
	recs, err := db.ListInstalls(context.Background(), domain.ScopeProject, "")
	if err != nil {
		t.Fatalf("ListInstalls: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("install rows left behind after rollback: %+v", recs)
	}
}

// A5 for the skill path: a successful skill install records node-level ledger
// rows (pre-image "", post-image the content digest), and removal deletes the
// directory, the skills-ledger row and the state rows together.
func TestInstallRemoveSkillIsSymmetric(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	project := t.TempDir()
	home := t.TempDir()
	agent, destDir := projectAgent(t, project, home)

	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "symmetric removal fixture")
	listing := skillListingFor("skill:example:demo-skill", "demo-skill", skillDir)

	outcome, err := installSkillFromListing(authorizedTestContext(), db, dataRoot, project, home,
		listing, "1.0.0", domain.ScopeProject, []string{agent})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	muts := mutationsOf(t, db, outcome.InstallID)
	if len(muts) != 1 {
		t.Fatalf("expected one ledger row for one skill directory, got %d", len(muts))
	}
	if muts[0].StructureType != "skill-dir" || muts[0].PreImageHash != "" {
		t.Errorf("unexpected mutation: %+v", muts[0])
	}
	if muts[0].PostImageHash != outcome.ContentDigest {
		t.Errorf("post-image %q is not the content digest %q", muts[0].PostImageHash, outcome.ContentDigest)
	}

	if err := removeInstalledPackage(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	skillDest := filepath.Join(destDir, "demo-skill")
	if _, err := os.Stat(skillDest); !os.IsNotExist(err) {
		t.Errorf("skill directory survived removal: %v", err)
	}
	ledger, lerr := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if lerr != nil {
		t.Fatalf("open skills ledger: %v", lerr)
	}
	if entries, lerr := ledger.All(); lerr != nil {
		t.Fatalf("read skills ledger: %v", lerr)
	} else if len(entries) != 0 {
		t.Errorf("skills ledger rows survived removal: %+v", entries)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err == nil {
		t.Error("install row survived removal")
	}
}
