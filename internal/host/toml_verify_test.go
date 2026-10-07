package host

// toml_verify_test.go — VerifySetup must not report a commented-out bridge
// as registered, and must report a live one.
//
// The regression under test: the TOML hosts answered with
// strings.Contains(content, "[mcp_servers.litespm]") && strings.Contains(content, "bridge"),
// so a config whose bridge table the user had commented out — header text and
// the word "bridge" both still present, as text — reported ready and no setup
// was offered.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commentedBridgeConfig carries every token the old substring check searched
// for, all inside comments: the header, the word "bridge", the command and
// the args. Only a comment-aware reader can call this "missing".
const commentedBridgeConfig = `# bridge disabled by hand
#[mcp_servers.litespm]
#command = "/opt/litespm/litespm"
#args = ["bridge", "stdio", "--host", "codex"]

[mcp_servers.other]
command = "echo"
args = ["bridge"]
`

// liveBridgeConfig is a real registration: a live header plus a live line in
// that section carrying the bridge argv.
const liveBridgeConfig = `[mcp_servers.litespm]
command = "/opt/litespm/litespm"
args = ["bridge", "stdio", "--host", "codex"]

[mcp_servers.other]
command = "echo"
`

// tomlTestTarget pairs a TOML adapter with the config file it reads.
type tomlTestTarget struct {
	name    string
	adapter HostAdapter
	path    string
}

// tomlTestTargets returns the TOML adapters under test: the two bespoke ones
// and one data-driven GenericAdapter row (the registered table currently
// contains no TOML target, so a synthetic row exercises that branch).
func tomlTestTargets(t *testing.T, home string) []tomlTestTarget {
	t.Helper()
	return []tomlTestTarget{
		{
			name:    "codex",
			adapter: &CodexAdapter{},
			path:    filepath.Join(home, ".codex", "config.toml"),
		},
		{
			name:    "grok-build",
			adapter: &GrokBuildAdapter{},
			path:    filepath.Join(home, ".grok", "config.toml"),
		},
		{
			name: "generic-toml",
			adapter: NewGenericAdapter(BridgeTarget{
				ID:      "toml-test",
				Name:    "TOML Test",
				Format:  FormatTOML,
				UserKey: []string{"mcp_servers"},
				UserPath: func(home string) string {
					return filepath.Join(home, ".tomltest", "config.toml")
				},
				DocsURL: "https://example.invalid/docs",
			}),
			path: filepath.Join(home, ".tomltest", "config.toml"),
		},
	}
}

func TestVerifySetupTOMLIgnoresCommentedTable(t *testing.T) {
	home := useTempHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	ctx := context.Background()

	for _, tc := range tomlTestTargets(t, home) {
		t.Run(tc.name, func(t *testing.T) {
			writeConfig(t, tc.path, commentedBridgeConfig)

			verify, err := tc.adapter.VerifySetup(ctx)
			if err != nil {
				t.Fatalf("VerifySetup: %v", err)
			}
			if verify.Registered || verify.Status != "missing" {
				t.Fatalf("a commented-out table must report missing, got registered=%v status=%q",
					verify.Registered, verify.Status)
			}

			// The same file with a live table must report ready, so the
			// comment-aware check is not simply always false.
			writeConfig(t, tc.path, liveBridgeConfig)
			verify, err = tc.adapter.VerifySetup(ctx)
			if err != nil {
				t.Fatalf("VerifySetup: %v", err)
			}
			if !verify.Registered || verify.Status != "ready" {
				t.Fatalf("a live bridge table must report ready, got registered=%v status=%q",
					verify.Registered, verify.Status)
			}
		})
	}
}

// TestPlanSetupReplacesCommentedTOMLTable pins the write half of the same bug:
// with only a commented-out table on disk, PlanSetup used to enter its "update
// the existing section" branch, match no live header, and propose the file
// unchanged — so setup ran and nothing was registered.
func TestPlanSetupReplacesCommentedTOMLTable(t *testing.T) {
	home := useTempHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	ctx := context.Background()
	backups := filepath.Join(home, "backups")

	cases := []struct {
		name    string
		adapter HostAdapter
		path    string
	}{
		{"codex", &CodexAdapter{}, filepath.Join(home, ".codex", "config.toml")},
		{"grok-build", &GrokBuildAdapter{}, filepath.Join(home, ".grok", "config.toml")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeConfig(t, tc.path, commentedBridgeConfig)

			plan, err := tc.adapter.PlanSetup(ctx, "/opt/litespm/litespm", backups)
			if err != nil {
				t.Fatalf("PlanSetup: %v", err)
			}
			if plan.ProposedContent == plan.OriginalContent {
				t.Fatalf("plan proposes no change for a commented-out table:\n%s", plan.ProposedContent)
			}
			if !tomlTableRegistered(plan.ProposedContent, "[mcp_servers.litespm]") {
				t.Fatalf("plan does not register a live table:\n%s", plan.ProposedContent)
			}
			if _, err := tc.adapter.ApplySetup(ctx, plan); err != nil {
				t.Fatalf("ApplySetup: %v", err)
			}
			verify, err := tc.adapter.VerifySetup(ctx)
			if err != nil {
				t.Fatalf("VerifySetup: %v", err)
			}
			if !verify.Registered {
				t.Fatalf("setup over a commented-out table did not register: %+v", verify)
			}
			// The user's own commented note and their other server survive.
			written, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(written)
			for _, want := range []string{"# bridge disabled by hand", "[mcp_servers.other]"} {
				if !strings.Contains(got, want) {
					t.Errorf("user content %q was lost:\n%s", want, got)
				}
			}
		})
	}
}

func TestStripTOMLComment(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`[mcp_servers.litespm] # disabled`, `[mcp_servers.litespm]`},
		{`#[mcp_servers.litespm]`, ``},
		{`  # a full line comment`, ``},
		{`command = "app#1"`, `command = "app#1"`},
		{`args = ["bridge"] # trailing`, `args = ["bridge"]`},
		{`no comment here`, `no comment here`},
		{`command = "it's fine" # note`, `command = "it's fine"`},
	}
	for _, c := range cases {
		if got := stripTOMLComment(c.in); got != c.want {
			t.Errorf("stripTOMLComment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTomlBridgeRegisteredScopesToTheTable(t *testing.T) {
	// "bridge" outside the litespm table must not register the host.
	besideTheTable := `[mcp_servers.other]
command = "bridge-runner"

[mcp_servers.litespm]
command = "/opt/litespm/litespm"
`
	if tomlBridgeRegistered(besideTheTable, "[mcp_servers.litespm]") {
		t.Error("a bridge word outside the litespm table must not count as registered")
	}
	if !tomlTableRegistered(besideTheTable, "[mcp_servers.litespm]") {
		t.Error("the live header itself must still be found")
	}

	// An inline comment mentioning bridge inside the table is a comment.
	commentOnly := "[mcp_servers.litespm]\ncommand = \"litespm\" # not a bridge arg\n"
	if tomlBridgeRegistered(commentOnly, "[mcp_servers.litespm]") {
		t.Error("a comment inside the table must not satisfy the bridge requirement")
	}

	// A header with a trailing comment is live.
	trailing := `[mcp_servers.litespm] # re-enabled
command = "/opt/litespm/litespm"
args = ["bridge"]
`
	if !tomlBridgeRegistered(trailing, "[mcp_servers.litespm]") {
		t.Error("a header with a trailing comment is a live header")
	}

	// The legacy name is its own table and never a registration.
	legacy := "[mcp_servers.litepsm]\nargs = [\"bridge\"]\n"
	if tomlBridgeRegistered(legacy, "[mcp_servers.litespm]") {
		t.Error("the legacy table must not satisfy the current name")
	}
}

// TestAdaptersStillDetectRealRegistrations is a guard against the sweep making
// VerifySetup stricter than what PlanSetup itself writes: the entry these
// adapters emit must verify as ready.
func TestAdaptersStillDetectRealRegistrations(t *testing.T) {
	home := useTempHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	ctx := context.Background()

	adapter := &CodexAdapter{}
	plan, err := adapter.PlanSetup(ctx, "/opt/litespm/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if _, err := adapter.ApplySetup(ctx, plan); err != nil {
		t.Fatalf("ApplySetup: %v", err)
	}
	verify, err := adapter.VerifySetup(ctx)
	if err != nil {
		t.Fatalf("VerifySetup: %v", err)
	}
	if !verify.Registered || verify.Status != "ready" {
		t.Fatalf("the entry PlanSetup writes must verify ready: %+v", verify)
	}
}

// ensure the synthetic target's table matches the real grammar.
func TestGenericTomlTargetTable(t *testing.T) {
	target := BridgeTarget{
		ID:      "toml-test",
		Format:  FormatTOML,
		UserKey: []string{"mcp_servers"},
	}
	if got, want := target.tomlTable(), "[mcp_servers.litespm]"; got != want {
		t.Fatalf("tomlTable() = %q, want %q", got, want)
	}
}
