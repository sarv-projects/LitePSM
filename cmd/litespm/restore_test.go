package main

// restore_test.go — acceptance for register item A2: `litespm restore` rolls
// an install back to its EXACT pre-install bytes, and refuses (changing
// nothing) whenever a byte-exact replay would discard anything LiteSPM does
// not own or anything the user edited afterwards.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/skills"
)

func setRestoreEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CODEX_HOME", "")
	return home
}

// The exact-rollback path: after a force install over the user's own entry,
// restore puts the original file back byte for byte and clears the state.
func TestRestoreRollsBackExactPreInstallBytes(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := setRestoreEnv(t)

	configPath := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"},"demo-mcp":{"command":"my-own-cmd"}}}`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, true, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}

	// Planning alone must not touch anything (this is --dry-run's engine).
	beforePlan := string(fileBytes(t, configPath))
	plan, err := planRestore(context.Background(), db, outcome.InstallID)
	if err != nil {
		t.Fatalf("planRestore: %v", err)
	}
	if got := string(fileBytes(t, configPath)); got != beforePlan {
		t.Fatalf("planning modified the config:\nbefore: %s\nafter:  %s", beforePlan, got)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err != nil {
		t.Fatalf("planning deleted state: %v", err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Created {
		t.Fatalf("unexpected plan: %+v", plan)
	}

	out, err := restoreInstall(context.Background(), db, dataRoot, outcome.InstallID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(out.Restored) != 1 {
		t.Errorf("expected one restored file, got %+v", out)
	}
	if got := string(fileBytes(t, configPath)); got != original {
		t.Errorf("not byte-identical to pre-install:\nwant: %s\ngot:  %s", original, got)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err == nil {
		t.Error("install row survived restore")
	}
	regs, err := db.ListHostRegistrationsForEntry(context.Background(), domain.ScopeUser, "demo-mcp")
	if err != nil {
		t.Fatalf("ListHostRegistrationsForEntry: %v", err)
	}
	if len(regs) != 0 {
		t.Errorf("host registration survived restore: %+v", regs)
	}
	if muts := mutationsOf(t, db, outcome.InstallID); len(muts) != 0 {
		t.Errorf("deployment rows survived restore: %+v", muts)
	}
}

// The second exact-rollback shape: a config the install did not create and
// whose only change was the added entry.
func TestRestoreRemovesEntryFromUntouchedConfig(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := setRestoreEnv(t)

	configPath := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"}}}`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := restoreInstall(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := string(fileBytes(t, configPath)); got != original {
		t.Errorf("not byte-identical to pre-install:\nwant: %s\ngot:  %s", original, got)
	}
}

// Guard 2: a sibling change since the install (by the user or by the host
// application) makes a byte-exact restore refuse and change nothing.
func TestRestoreRefusesWhenSiblingChanged(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := setRestoreEnv(t)

	configPath := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"}}}`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// The host application (or the user) writes where LiteSPM does not own:
	// a new root key and a field on a sibling entry.
	doc := map[string]any{}
	if err := json.Unmarshal(fileBytes(t, configPath), &doc); err != nil {
		t.Fatal(err)
	}
	doc["userID"] = "abc123"
	servers, _ := doc["mcpServers"].(map[string]any)
	if mine, ok := servers["mine"].(map[string]any); ok {
		mine["note"] = "mine"
	} else {
		t.Fatal("fixture: sibling entry missing")
	}
	editedBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, editedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	edited := string(editedBytes)
	beforeState, err := db.GetInstall(context.Background(), outcome.InstallID)
	if err != nil {
		t.Fatalf("install state: %v", err)
	}

	_, err = restoreInstall(context.Background(), db, dataRoot, outcome.InstallID)
	if err == nil {
		t.Fatal("expected a refusal: the file changed outside owned entries")
	}
	if !strings.Contains(err.Error(), "changed outside LiteSPM-owned entries") {
		t.Errorf("refusal must say what differs, got: %v", err)
	}
	if !strings.Contains(err.Error(), "install remove") {
		t.Errorf("refusal must point at the surgical alternative, got: %v", err)
	}
	if got := string(fileBytes(t, configPath)); got != edited {
		t.Errorf("refused restore modified the config:\n%s", got)
	}
	afterState, err := db.GetInstall(context.Background(), outcome.InstallID)
	if err != nil || afterState.InstallID != beforeState.InstallID {
		t.Errorf("refused restore touched state: %+v err=%v", afterState, err)
	}
}

// Guard 1: an entry LiteSPM wrote that the user edited afterwards is theirs
// now — restore refuses rather than overwriting the edit.
func TestRestoreRefusesWhenOwnedNodeEdited(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := setRestoreEnv(t)

	configPath := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]}}}`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// The user rewrites the entry LiteSPM installed.
	doc := map[string]any{}
	if err := json.Unmarshal(fileBytes(t, configPath), &doc); err != nil {
		t.Fatal(err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	entry, _ := servers["demo-mcp"].(map[string]any)
	if entry == nil {
		t.Fatal("fixture: installed entry missing")
	}
	entry["command"] = "user-override"
	mutated, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	edited := string(mutated)

	_, err = restoreInstall(context.Background(), db, dataRoot, outcome.InstallID)
	if err == nil {
		t.Fatal("expected a refusal: the owned node was edited after the install")
	}
	if !strings.Contains(err.Error(), "edited after the install") {
		t.Errorf("refusal must explain the edited node, got: %v", err)
	}
	if got := string(fileBytes(t, configPath)); got != edited {
		t.Errorf("refused restore modified the config:\n%s", got)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err != nil {
		t.Errorf("refused restore deleted state: %v", err)
	}
}

// A config the install created is removed again — unless the user added
// something to it first, in which case it refuses.
func TestRestoreRemovesCreatedConfigAndRefusesAdditions(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := setRestoreEnv(t)
	configPath := filepath.Join(home, ".claude.json")

	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("install did not create the config: %v", err)
	}
	if _, err := restoreInstall(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("install-created config survived restore: %v", err)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err == nil {
		t.Error("install row survived restore")
	}

	// The user put something of their own into the created config.
	outcome2, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:other", "other-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(fileBytes(t, configPath), &doc); err != nil {
		t.Fatal(err)
	}
	doc["userKey"] = "keep-me"
	withUserBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, withUserBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	withUser := string(withUserBytes)
	_, err = restoreInstall(context.Background(), db, dataRoot, outcome2.InstallID)
	if err == nil {
		t.Fatal("expected refusal: the created config now holds user content")
	}
	if !strings.Contains(err.Error(), "changed outside LiteSPM-owned entries") {
		t.Errorf("unexpected refusal: %v", err)
	}
	if got := string(fileBytes(t, configPath)); got != withUser {
		t.Errorf("refused restore modified the config:\n%s", got)
	}
}

// TOML hosts take the same path with section-level comparison.
func TestRestoreRollsBackTOMLConfig(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	home := setRestoreEnv(t)

	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# my codex config\n[mcp_servers.litespm]\ncommand = \"/usr/local/bin/litespm\"\nargs = [\"bridge\", \"stdio\", \"--host\", \"codex\"]\n\n[mcp_servers.mine]\ncommand = \"npx\"\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"codex"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := restoreInstall(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := string(fileBytes(t, configPath)); got != original {
		t.Errorf("not byte-identical to pre-install:\nwant: %s\ngot:  %s", original, got)
	}

	// A sibling TOML section changed since the install → refuse.
	outcome2, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:other", "other-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"codex"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	edited := string(fileBytes(t, configPath)) + "\n[other_section]\nkeep = true\n"
	if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = restoreInstall(context.Background(), db, dataRoot, outcome2.InstallID)
	if err == nil {
		t.Fatal("expected refusal: an unowned TOML section appeared since the install")
	}
	if !strings.Contains(err.Error(), "other_section") {
		t.Errorf("refusal must name the differing section, got: %v", err)
	}
	if got := string(fileBytes(t, configPath)); got != edited {
		t.Errorf("refused restore modified the config:\n%s", got)
	}
}

// The skill path: a directory whose digest still matches is removed with its
// ledger row; one the user touched is refused.
func TestRestoreSkillDirectoryAndRefusesEdited(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	project := t.TempDir()
	home := t.TempDir()
	agent, destDir := projectAgent(t, project, home)

	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "restore fixture")
	listing := skillListingFor("skill:example:demo-skill", "demo-skill", skillDir)

	outcome, err := installSkillFromListing(authorizedTestContext(), db, dataRoot, project, home,
		listing, "1.0.0", domain.ScopeProject, []string{agent})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	skillDest := filepath.Join(destDir, "demo-skill")

	// First: the user edits the installed skill → refuse.
	if err := os.WriteFile(filepath.Join(skillDest, "EXTRA.md"), []byte("my note"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreInstall(context.Background(), db, dataRoot, outcome.InstallID); err == nil {
		t.Fatal("expected refusal: the skill directory changed since the install")
	} else if !strings.Contains(err.Error(), "changed since the install") {
		t.Errorf("unexpected refusal: %v", err)
	}
	if _, err := os.Stat(skillDest); err != nil {
		t.Errorf("refused restore removed the directory: %v", err)
	}

	// Undo the edit: back to exactly what LiteSPM wrote.
	if err := os.Remove(filepath.Join(skillDest, "EXTRA.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreInstall(context.Background(), db, dataRoot, outcome.InstallID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(skillDest); !os.IsNotExist(err) {
		t.Errorf("skill directory survived restore: %v", err)
	}
	ledger, err := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if err != nil {
		t.Fatalf("open skills ledger: %v", err)
	}
	if entries, err := ledger.All(); err != nil {
		t.Fatalf("read skills ledger: %v", err)
	} else if len(entries) != 0 {
		t.Errorf("skills ledger rows survived restore: %+v", entries)
	}
	if _, err := db.GetInstall(context.Background(), outcome.InstallID); err == nil {
		t.Error("install row survived restore")
	}
}
