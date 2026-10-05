package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/config"
)

func TestParseSkillsRemoveArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, opts skillsRemoveOptions)
	}{
		{
			name: "flags",
			args: []string{"pdf", "--scope", "global", "--force", "--dry-run", "--json"},
			check: func(t *testing.T, opts skillsRemoveOptions) {
				if len(opts.names) != 1 || opts.names[0] != "pdf" {
					t.Errorf("names=%v", opts.names)
				}
				if opts.scope != "global" || !opts.scopeGiven || !opts.force || !opts.dryRun || !opts.jsonOut {
					t.Errorf("unexpected opts: %+v", opts)
				}
			},
		},
		{
			name: "all and global shorthand",
			args: []string{"--all", "-g"},
			check: func(t *testing.T, opts skillsRemoveOptions) {
				if !opts.all || opts.scope != "global" || !opts.scopeGiven {
					t.Errorf("unexpected opts: %+v", opts)
				}
			},
		},
		{
			name: "default scope is project",
			args: []string{"--all"},
			check: func(t *testing.T, opts skillsRemoveOptions) {
				if opts.scope != "project" {
					t.Errorf("default scope = %q, want project", opts.scope)
				}
			},
		},
		{name: "help", args: []string{"--help"}, check: func(t *testing.T, opts skillsRemoveOptions) {
			if !opts.help {
				t.Error("help not set")
			}
		}},
		{name: "unknown flag", args: []string{"pdf", "--nope"}, wantErr: true},
		{name: "invalid scope", args: []string{"pdf", "--scope", "user"}, wantErr: true},
		{name: "missing scope value", args: []string{"pdf", "--scope"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseSkillsRemoveArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", opts)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, opts)
			}
		})
	}
}

// setupRemoval installs one project-scoped and one global-scoped skill under a
// fresh data root and returns the harness paths plus both directories.
func setupRemoval(t *testing.T) (paths *config.PlatformPaths, projectDir, globalDir string) {
	t.Helper()
	dataRoot := t.TempDir()
	projectDir = installTestSkill(t, dataRoot, "pdf", "project", "v1 body")
	globalDir = installTestSkill(t, dataRoot, "docx", "global", "v1 body")
	return &config.PlatformPaths{DataRoot: dataRoot}, projectDir, globalDir
}

func TestExecuteSkillsRemove_ScopeAndForce(t *testing.T) {
	t.Run("scope project refuses the global entry", func(t *testing.T) {
		paths, projectDir, globalDir := setupRemoval(t)
		outcomes, err := executeSkillsRemove(skillsRemoveOptions{
			all: true, scope: "project", scopeGiven: true, dryRun: true,
		}, paths)
		if err != nil {
			t.Fatalf("executeSkillsRemove failed: %v", err)
		}
		if len(outcomes) != 2 {
			t.Fatalf("expected 2 outcomes, got %+v", outcomes)
		}
		for _, o := range outcomes {
			if o.Entry.SkillName == "docx" {
				if o.Removed {
					t.Fatal("a scope-mismatched entry must not be removed")
				}
				if !strings.Contains(o.Reason, "scope mismatch") {
					t.Errorf("reason %q must explain the scope refusal", o.Reason)
				}
			}
		}
		// Dry run: both directories must still exist.
		for _, dir := range []string{projectDir, globalDir} {
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("dry-run removed %s: %v", dir, err)
			}
		}
	})

	t.Run("default scope keeps the global entry and removes the project one", func(t *testing.T) {
		paths, projectDir, globalDir := setupRemoval(t)
		outcomes, err := executeSkillsRemove(skillsRemoveOptions{all: true}, paths)
		if err != nil {
			t.Fatalf("executeSkillsRemove failed: %v", err)
		}
		if len(outcomes) != 2 {
			t.Fatalf("expected 2 outcomes, got %+v", outcomes)
		}
		for _, o := range outcomes {
			switch o.Entry.SkillName {
			case "pdf":
				if !o.Removed {
					t.Errorf("project-scoped pdf was not removed: %s", o.Reason)
				}
			case "docx":
				if o.Removed {
					t.Fatal("a global-scoped entry must not be removed by the project default")
				}
				if !strings.Contains(o.Reason, "scope mismatch") {
					t.Errorf("reason %q must explain the scope refusal", o.Reason)
				}
			}
		}
		if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
			t.Error("project directory was not removed")
		}
		if _, err := os.Stat(globalDir); err != nil {
			t.Errorf("global directory must be left in place: %v", err)
		}
	})

	t.Run("force overrides the user-added-file guard", func(t *testing.T) {
		paths, projectDir, _ := setupRemoval(t)
		extra := filepath.Join(projectDir, "notes.txt")
		if err := os.WriteFile(extra, []byte("user notes\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		outcomes, err := executeSkillsRemove(skillsRemoveOptions{names: []string{"pdf"}}, paths)
		if err != nil {
			t.Fatalf("executeSkillsRemove failed: %v", err)
		}
		if len(outcomes) != 1 || outcomes[0].Removed {
			t.Fatalf("user-added content must be refused without --force: %+v", outcomes)
		}
		if !strings.Contains(outcomes[0].Reason, "not recorded at install") {
			t.Errorf("refusal reason %q should name the extra file", outcomes[0].Reason)
		}
		if _, err := os.Stat(extra); err != nil {
			t.Errorf("refused removal must leave user content in place: %v", err)
		}

		outcomes, err = executeSkillsRemove(skillsRemoveOptions{names: []string{"pdf"}, force: true}, paths)
		if err != nil {
			t.Fatalf("executeSkillsRemove --force failed: %v", err)
		}
		if len(outcomes) != 1 || !outcomes[0].Removed {
			t.Fatalf("--force must allow removal: %+v", outcomes)
		}
		if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
			t.Error("forced removal did not delete the directory")
		}
	})
}
