package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/skills"
)

// writeTestSkill writes a valid SKILL.md into dir.
func writeTestSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test skill for " + name + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// installTestSkill records a real skill install in a fresh ledger under
// dataRoot and returns the installed directory.
func installTestSkill(t *testing.T, dataRoot, name, scope, body string) string {
	t.Helper()
	ledger, err := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), name)
	writeTestSkill(t, src, name, body)
	dst := filepath.Join(t.TempDir(), name)
	if _, err := skills.NewInstaller(ledger, nil).Install(context.Background(), skills.InstallRequest{
		Op:     skills.InstallOp{SkillName: name, FromDir: src, ToDir: dst, HostLabel: "opencode"},
		Source: skills.SkillSource{Display: "owner/repo"},
		Scope:  scope,
	}); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestParseSkillsUpdateArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, opts skillsUpdateOptions)
	}{
		{
			name: "all flags",
			args: []string{"pdf", "docx", "--source", "owner/repo", "--ref", "v2", "--scope", "global", "--force", "--dry-run", "--json", "--yes"},
			check: func(t *testing.T, opts skillsUpdateOptions) {
				if len(opts.names) != 2 || opts.names[0] != "pdf" || opts.names[1] != "docx" {
					t.Errorf("names=%v", opts.names)
				}
				if opts.source != "owner/repo" || opts.ref != "v2" || opts.scope != "global" || !opts.scopeGiven {
					t.Errorf("unexpected opts: %+v", opts)
				}
				if !opts.force || !opts.dryRun || !opts.jsonOut || !opts.yes {
					t.Errorf("flags not parsed: %+v", opts)
				}
			},
		},
		{
			name: "defaults",
			args: []string{"pdf", "-s", "owner/repo"},
			check: func(t *testing.T, opts skillsUpdateOptions) {
				if opts.scopeGiven || opts.force || opts.dryRun || opts.jsonOut || opts.yes {
					t.Errorf("unexpected defaults: %+v", opts)
				}
			},
		},
		{name: "help", args: []string{"--help"}, check: func(t *testing.T, opts skillsUpdateOptions) {
			if !opts.help {
				t.Error("help not set")
			}
		}},
		{name: "unknown flag", args: []string{"pdf", "-s", "x", "--nope"}, wantErr: true},
		{name: "invalid scope", args: []string{"pdf", "-s", "x", "--scope", "user"}, wantErr: true},
		{name: "missing source value", args: []string{"pdf", "--source"}, wantErr: true},
		{name: "missing ref value", args: []string{"pdf", "-s", "x", "--ref"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseSkillsUpdateArgs(tc.args)
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

func TestExecuteSkillsUpdate_LocalSource(t *testing.T) {
	dataRoot := t.TempDir()
	installedDir := installTestSkill(t, dataRoot, "pdf", "project", "v1 body")
	paths := &config.PlatformPaths{DataRoot: dataRoot}
	installedFile := filepath.Join(installedDir, "SKILL.md")

	srcV2 := filepath.Join(t.TempDir(), "pdf")
	writeTestSkill(t, srcV2, "pdf", "v2 body")
	srcV2File := filepath.Join(srcV2, "SKILL.md")

	var updatedDigest string

	t.Run("dry run reports would_update and changes nothing", func(t *testing.T) {
		before, err := os.ReadFile(installedFile)
		if err != nil {
			t.Fatal(err)
		}
		results, err := executeSkillsUpdate(context.Background(), skillsUpdateOptions{
			names: []string{"pdf"}, source: srcV2, dryRun: true, yes: true,
		}, paths)
		if err != nil {
			t.Fatalf("executeSkillsUpdate failed: %v", err)
		}
		if len(results) != 1 || results[0].Status != "would_update" {
			t.Fatalf("unexpected results: %+v", results)
		}
		after, _ := os.ReadFile(installedFile)
		if string(before) != string(after) {
			t.Fatal("dry-run must not modify the installed tree")
		}
	})

	t.Run("real update writes content and refreshes the ledger", func(t *testing.T) {
		results, err := executeSkillsUpdate(context.Background(), skillsUpdateOptions{
			names: []string{"pdf"}, source: srcV2, yes: true,
		}, paths)
		if err != nil {
			t.Fatalf("executeSkillsUpdate failed: %v", err)
		}
		if len(results) != 1 || results[0].Status != "updated" {
			t.Fatalf("unexpected results: %+v", results)
		}
		updatedDigest = results[0].ContentDigest
		if updatedDigest == "" {
			t.Fatalf("provenance not recorded: %+v", results[0])
		}
		got, err := os.ReadFile(installedFile)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := os.ReadFile(srcV2File)
		if string(got) != string(want) {
			t.Fatalf("installed content not replaced:\n got %q\nwant %q", got, want)
		}
		ledger, _ := skills.OpenLedger(skills.LedgerPath(dataRoot))
		entries, _ := ledger.All()
		if len(entries) != 1 || entries[0].ContentDigest != updatedDigest {
			t.Fatalf("ledger not refreshed: %+v", entries)
		}
	})

	t.Run("second run is already_current", func(t *testing.T) {
		results, err := executeSkillsUpdate(context.Background(), skillsUpdateOptions{
			names: []string{"pdf"}, source: srcV2, yes: true,
		}, paths)
		if err != nil {
			t.Fatalf("executeSkillsUpdate failed: %v", err)
		}
		if len(results) != 1 || results[0].Status != "already_current" {
			t.Fatalf("expected already_current, got %+v", results)
		}
	})

	t.Run("scope mismatch is skipped", func(t *testing.T) {
		globalRoot := t.TempDir()
		globalDir := installTestSkill(t, globalRoot, "pdf", "global", "v1 body")
		globalPaths := &config.PlatformPaths{DataRoot: globalRoot}
		results, err := executeSkillsUpdate(context.Background(), skillsUpdateOptions{
			names: []string{"pdf"}, source: srcV2, scope: "project", scopeGiven: true, yes: true,
		}, globalPaths)
		if err != nil {
			t.Fatalf("executeSkillsUpdate failed: %v", err)
		}
		if len(results) != 1 || results[0].Status != "skipped" {
			t.Fatalf("expected skipped, got %+v", results)
		}
		if !strings.Contains(results[0].Reason, "scope mismatch") {
			t.Errorf("reason %q must explain the scope refusal", results[0].Reason)
		}
		if _, err := os.Stat(globalDir); err != nil {
			t.Errorf("a scope-mismatched entry must be left in place: %v", err)
		}
	})

	t.Run("unknown installed name is not_installed", func(t *testing.T) {
		results, err := executeSkillsUpdate(context.Background(), skillsUpdateOptions{
			names: []string{"does-not-exist"}, source: srcV2, yes: true,
		}, paths)
		if err != nil {
			t.Fatalf("executeSkillsUpdate failed: %v", err)
		}
		if len(results) != 1 || results[0].Status != "not_installed" {
			t.Fatalf("expected not_installed, got %+v", results)
		}
	})

	t.Run("ref on a local source is rejected", func(t *testing.T) {
		if _, err := executeSkillsUpdate(context.Background(), skillsUpdateOptions{
			names: []string{"pdf"}, source: srcV2, ref: "main", yes: true,
		}, paths); err == nil {
			t.Fatal("expected --ref on a local source to be rejected")
		}
	})
}

func TestEnginePolicyChecker(t *testing.T) {
	req := skills.PolicyRequest{
		Operation: "update",
		SkillName: "pdf",
		Scope:     "global",
		DestDir:   "/tmp/pdf",
		Effects:   []string{"filesystem.write", "filesystem.delete"},
	}

	t.Run("input mapping", func(t *testing.T) {
		in := skillPolicyInput(req)
		if in.Operation != "update" || in.TargetRef != "pdf" {
			t.Errorf("unexpected input: %+v", in)
		}
		if in.Scope != "user" {
			t.Errorf("global scope must map to user, got %q", in.Scope)
		}
		if len(in.Effects) != 2 || in.Effects[0].Effect != policy.EffectFilesystemWrite || in.Effects[1].Effect != policy.EffectFilesystemDelete {
			t.Errorf("effects not mapped: %+v", in.Effects)
		}
	})

	t.Run("ask denied when resolver declines", func(t *testing.T) {
		checker := enginePolicyChecker(policy.NewEngine(nil, nil), func(context.Context, skills.PolicyRequest, string) bool { return false })
		verdict := checker(context.Background(), req)
		if verdict.Allowed {
			t.Fatalf("an unapproved ask must not be allowed: %+v", verdict)
		}
		if verdict.Decision != string(policy.DecisionAsk) {
			t.Errorf("decision=%q, want ask", verdict.Decision)
		}
	})

	t.Run("ask allowed only with an explicit resolver approval", func(t *testing.T) {
		checker := enginePolicyChecker(policy.NewEngine(nil, nil), func(context.Context, skills.PolicyRequest, string) bool { return true })
		verdict := checker(context.Background(), req)
		if !verdict.Allowed {
			t.Fatalf("an approved ask must be allowed: %+v", verdict)
		}
	})

	t.Run("explicit deny is never resolved by the asker", func(t *testing.T) {
		engine := policy.NewEngine(nil, []policy.DenyRule{{RuleID: "r1", TargetRef: "pdf"}})
		checker := enginePolicyChecker(engine, func(context.Context, skills.PolicyRequest, string) bool { return true })
		verdict := checker(context.Background(), req)
		if verdict.Allowed {
			t.Fatalf("a deny must not be bypassed by the asker: %+v", verdict)
		}
		if verdict.Decision != string(policy.DecisionDeny) {
			t.Errorf("decision=%q, want deny", verdict.Decision)
		}
	})

	t.Run("nil engine yields nil checker", func(t *testing.T) {
		if enginePolicyChecker(nil, nil) != nil {
			t.Fatal("a nil engine must not produce a permissive checker")
		}
	})
}
