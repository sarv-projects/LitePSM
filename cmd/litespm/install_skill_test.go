package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

func openTestState(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func skillListingFor(id, name, source string) *domain.Listing {
	return &domain.Listing{
		SchemaVersion: 1,
		ID:            id,
		Kind:          domain.KindSkill,
		Name:          name,
		Summary:       "test fixture",
		Source:        domain.SourceReference{SourceID: "example", UpstreamID: name, URL: source},
		Status:        domain.ListingStatusActive,
	}
}

// projectAgent returns an agent target with a project-scope skill directory and
// the directory it resolves to.
func projectAgent(t *testing.T, project, home string) (string, string) {
	t.Helper()
	for _, a := range skills.AgentTargets() {
		if dir, ok := skills.AgentSkillDir(a.ID, "project", project, home); ok {
			return a.ID, dir
		}
	}
	t.Skip("no agent target exposes a project-scope skill directory")
	return "", ""
}

// TestInstallSkillFromListingLocal is the hermetic proof that a catalog skill
// listing installs real files: a local skill directory stands in for a cloned
// source, and the test asserts the write, the ledger entry, and the persisted
// install record that `list_installed` reads.
func TestInstallSkillFromListingLocal(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	project := t.TempDir()
	home := t.TempDir()
	agent, destDir := projectAgent(t, project, home)

	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "A demo skill for the install test")

	listing := skillListingFor("skill:example:demo-skill", "demo-skill", skillDir)
	outcome, err := installSkillFromListing(context.Background(), db, dataRoot, project, home,
		listing, "1.0.0", domain.ScopeProject, []string{agent})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if outcome.SkillName != "demo-skill" {
		t.Errorf("skill name %q, want demo-skill", outcome.SkillName)
	}
	if outcome.ContentDigest == "" {
		t.Error("no content digest captured")
	}
	if len(outcome.LedgerEntries) == 0 {
		t.Fatal("no ledger entries recorded")
	}
	if _, err := os.Stat(filepath.Join(destDir, "demo-skill", "SKILL.md")); err != nil {
		t.Errorf("expected SKILL.md under %s: %v", destDir, err)
	}

	rec, err := db.GetInstall(context.Background(), outcome.InstallID)
	if err != nil {
		t.Fatalf("install record not persisted: %v", err)
	}
	if rec.ListingID != listing.ID || rec.Status != domain.InstallActive {
		t.Errorf("install record = %+v", rec)
	}
}

// TestInstallExecuteSkillThroughDaemon is the agent-facing path end to end:
// prepare_install resolves a skill listing, request_install (install.execute)
// installs it as files into the detected agent's skill directory, and the
// install record is persisted for list_installed.
func TestInstallExecuteSkillThroughDaemon(t *testing.T) {
	// The handler resolves the home directory from the environment, so the test
	// owns it: a temp home means detection and destinations are isolated.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	for _, a := range skills.AgentTargets() {
		if a.EnvVar != "" {
			t.Setenv(a.EnvVar, "")
		}
	}
	// Make exactly one agent detectable: cursor's global dir is home-relative.
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "installed through the daemon")
	const listingID = "skill:test:demo-skill"
	listing := &domain.Listing{
		SchemaVersion: 2,
		ID:            listingID,
		Kind:          domain.KindSkill,
		Name:          "demo-skill",
		Summary:       "daemon install fixture",
		Source:        domain.SourceReference{SourceID: "test", UpstreamID: "demo-skill", URL: skillDir},
		Versions:      []domain.VersionSummary{{Version: "1.0.0"}},
	}
	h := newHarnessWith(t, []*domain.Listing{listing})
	ctx := context.Background()

	var plan domain.InstallPlan
	if err := h.client.Call(ctx, "resolver.prepare_plan", map[string]any{
		"id": listingID, "version": "1.0.0", "scope": "user",
	}, &plan); err != nil {
		t.Fatalf("prepare_plan failed: %v", err)
	}

	var result map[string]any
	if err := h.client.Call(ctx, "install.execute", map[string]any{"planId": plan.PlanID}, &result); err != nil {
		t.Fatalf("install.execute failed: %v", err)
	}
	if result["kind"] != "skill" {
		t.Errorf("expected a skill result, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "skills", "demo-skill", "SKILL.md")); err != nil {
		t.Errorf("skill not written to the detected agent directory: %v", err)
	}

	recs, err := h.db.ListInstalls(ctx, domain.ScopeUser, "")
	if err != nil {
		t.Fatalf("ListInstalls: %v", err)
	}
	found := false
	for _, r := range recs {
		if r.ListingID == listingID {
			found = true
		}
	}
	if !found {
		t.Errorf("install record for %s not persisted", listingID)
	}
}

// TestInstallSkillFromListingFailsClosed pins the honest failure modes: no
// source, a non-git source, and a name that is absent from a multi-skill
// source all report LPSM-ARTIFACT-UNAVAILABLE rather than inventing a package.
func TestInstallSkillFromListingFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		listing *domain.Listing
		prepare func(t *testing.T) string // returns the source URL
	}{
		{
			name:    "no source URL",
			listing: skillListingFor("skill:example:none", "none", ""),
			prepare: func(t *testing.T) string { return "" },
		},
		{
			name:    "non-git source",
			listing: skillListingFor("skill:example:none", "none", "https://officialskills.sh/microsoft/skills/x"),
			prepare: func(t *testing.T) string { return "https://officialskills.sh/microsoft/skills/x" },
		},
		{
			name:    "name absent from a multi-skill source",
			listing: skillListingFor("skill:example:missing", "missing", ""),
			prepare: func(t *testing.T) string {
				root := t.TempDir()
				writeTestSkill(t, filepath.Join(root, "one"), "one", "first")
				writeTestSkill(t, filepath.Join(root, "two"), "two", "second")
				return root
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.prepare(t)
			if tc.listing.Source.URL == "" {
				tc.listing.Source.URL = source
			}
			_, err := installSkillFromListing(context.Background(), openTestState(t), t.TempDir(),
				t.TempDir(), t.TempDir(), tc.listing, "1.0.0", domain.ScopeProject, []string{"codex"})
			if err == nil {
				t.Fatal("expected a failure")
			}
			var lpsm *domain.LPSMError
			if !errors.As(err, &lpsm) || lpsm.Code != "LPSM-ARTIFACT-UNAVAILABLE" {
				t.Fatalf("expected LPSM-ARTIFACT-UNAVAILABLE, got %v", err)
			}
		})
	}
}
