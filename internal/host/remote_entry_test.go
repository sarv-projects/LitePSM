package host

// remote_entry_test.go — B1 acceptance for remote (URL) MCP entries.
//
// Three questions are answered here, one per group of tests:
//
//   1. Does every capable host receive EXACTLY its own documented entry
//      spelling (golden text), and does a refusal stay fail-closed everywhere
//      else? — the per-host matrix of the B1 research report, PART 1 §1.3-§1.5.
//   2. Does the entry survive a read-back round trip (endpoint + transport)?
//   3. Do the content-based state/remove/restore helpers treat a remote entry
//      like any other owned node: byte-identical restore, surgical removal,
//      sibling entries untouched? — PART 4 Phase 2 Step 2.3.
//
// The golden strings are pinned byte-for-byte on purpose: "contains url" would
// pass for a writer that also emitted a bogus `command`, and the whole point of
// the writer rule is that a remote entry emits ONLY its URL key (plus the
// host's discriminator).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

const remoteTestURL = "https://mcp.example.com/mcp"

// remoteGoldenCase is one capable host: where its config lands under a pinned
// HOME, the exact bytes a from-empty install must write, and how the transport
// reads back (the host's own discriminator, or the registry token when the host
// infers the transport from the URL alone).
type remoteGoldenCase struct {
	hostID string
	// file is the config path relative to the sandbox home.
	file string
	// streamableHTTP is the EXACT file content after installing a
	// streamable-http remote entry into an empty machine.
	streamableHTTP string
	// sse is the exact content for an sse entry; "" means the host documents
	// no sse spelling for a URL entry (RefusedSSE is then the assertion).
	sse string
	// readBackTransport is what ListServerEntriesWithValues reports as
	// Transport for the streamable-http entry, before registry mapping.
	readBackTransport string
}

func remoteGoldenCases() []remoteGoldenCase {
	return []remoteGoldenCase{
		{
			// {"type":"http","url":"…"} — a url with no type is a
			// configuration error Claude Code skips and reports, so the
			// discriminator is mandatory here.
			hostID:            "claude-code",
			file:              ".claude.json",
			streamableHTTP:    "{\n  \"mcpServers\": {\n  \"demo\": {\"type\":\"http\",\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			sse:               "{\n  \"mcpServers\": {\n  \"demo\": {\"type\":\"sse\",\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			readBackTransport: "http",
		},
		{
			// {"type":"streamableHttp","url":"…"} — omitting `type` defaults
			// to legacy sse, so writing no discriminator would register the
			// wrong transport.
			hostID:            "cline",
			file:              ".cline/data/settings/cline_mcp_settings.json",
			streamableHTTP:    "{\n  \"mcpServers\": {\n  \"demo\": {\"type\":\"streamableHttp\",\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			sse:               "{\n  \"mcpServers\": {\n  \"demo\": {\"type\":\"sse\",\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			readBackTransport: "streamableHttp",
		},
		{
			hostID:            "opencode",
			file:              ".config/opencode/opencode.json",
			streamableHTTP:    "{\n  \"mcp\": {\n  \"demo\": {\"type\":\"remote\",\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			readBackTransport: "remote",
		},
		{
			hostID:            "codex",
			file:              ".codex/config.toml",
			streamableHTTP:    "[mcp_servers.demo]\nurl = \"" + remoteTestURL + "\"\n",
			readBackTransport: TransportStreamableHTTP,
		},
		{
			hostID:            "grok-build",
			file:              ".grok/config.toml",
			streamableHTTP:    "[mcp_servers.demo]\nurl = \"" + remoteTestURL + "\"\n",
			readBackTransport: TransportStreamableHTTP,
		},
		{
			hostID:            "pi-agent",
			file:              ".pi/agent/mcp.json",
			streamableHTTP:    "{\n  \"mcpServers\": {\n  \"demo\": {\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			readBackTransport: TransportStreamableHTTP,
		},
		{
			// Cursor infers streamable-http vs sse from the endpoint, so the
			// sse entry is byte-identical to the streamable-http one.
			hostID:            "cursor",
			file:              ".cursor/mcp.json",
			streamableHTTP:    "{\n  \"mcpServers\": {\n  \"demo\": {\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			sse:               "{\n  \"mcpServers\": {\n  \"demo\": {\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			readBackTransport: TransportStreamableHTTP,
		},
		{
			hostID:            "zed",
			file:              ".config/zed/settings.json",
			streamableHTTP:    "{\n  \"context_servers\": {\n  \"demo\": {\"url\":\"" + remoteTestURL + "\"}\n}\n}",
			readBackTransport: TransportStreamableHTTP,
		},
	}
}

// remoteHome sandboxes the machine and pins every per-host path override to its
// default under the temp home, so an ambient CODEX_HOME/GROK_HOME/… on the
// developer's box can never redirect a golden write.
func remoteHome(t *testing.T) string {
	t.Helper()
	home := entryHome(t)
	for key, val := range map[string]string{
		"CODEX_HOME":              "",
		"GROK_HOME":               "",
		"PI_CODING_AGENT_DIR":     "",
		"CLINE_MCP_SETTINGS_PATH": "",
		"CLINE_DATA_DIR":          "",
		"OPENCODE_CONFIG_DIR":     "",
	} {
		t.Setenv(key, val)
	}
	return home
}

func installRemote(t *testing.T, home, hostID string, entry ServerEntry, force bool) *EntryInstallResult {
	t.Helper()
	res, err := InstallServerEntry(context.Background(), hostID, entry, EntryInstallOptions{
		BackupDir: filepath.Join(home, "backups"),
		Scope:     domain.ScopeUser,
		Force:     force,
	})
	if err != nil {
		t.Fatalf("InstallServerEntry(%s): %v", hostID, err)
	}
	return res
}

// TestRemoteEntryGoldenText is Step 2.2's golden test: the exact written
// JSON/TOML text per capable host, for streamable-http everywhere and for sse
// only where the vendor documents one — plus the refusal (and no write at all)
// where the host documents no sse spelling.
func TestRemoteEntryGoldenText(t *testing.T) {
	for _, tc := range remoteGoldenCases() {
		t.Run(tc.hostID, func(t *testing.T) {
			t.Run("streamable-http", func(t *testing.T) {
				home := remoteHome(t)
				installRemote(t, home, tc.hostID, ServerEntry{
					Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
				}, false)
				data, err := os.ReadFile(filepath.Join(home, tc.file))
				if err != nil {
					t.Fatalf("read config: %v", err)
				}
				if string(data) != tc.streamableHTTP {
					t.Errorf("written config is not the golden text\n got: %q\nwant: %q", data, tc.streamableHTTP)
				}
				// A remote entry emits ONLY the URL key (+ discriminator):
				// never a launch line, never env.
				for _, forbidden := range []string{"command", "args", "env", "environment"} {
					if strings.Contains(string(data), forbidden) {
						t.Errorf("remote entry leaked a stdio field %q:\n%s", forbidden, data)
					}
				}
			})
			if tc.sse == "" {
				t.Run("sse-refused", func(t *testing.T) {
					home := remoteHome(t)
					_, err := InstallServerEntry(context.Background(), tc.hostID, ServerEntry{
						Name: "demo", Endpoint: remoteTestURL, Transport: TransportSSE,
					}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser})
					if err == nil {
						t.Fatal("sse install into a host with no verified sse spelling was not refused")
					}
					if !strings.Contains(err.Error(), RemoteUnsupportedCode) {
						t.Errorf("refusal is not the typed remote code: %v", err)
					}
					if !strings.Contains(err.Error(), tc.hostID) || !strings.Contains(err.Error(), TransportSSE) {
						t.Errorf("refusal must name host and transport: %v", err)
					}
					if _, statErr := os.Stat(filepath.Join(home, tc.file)); !os.IsNotExist(statErr) {
						t.Errorf("a refused remote install must write nothing (stat: %v)", statErr)
					}
				})
				return
			}
			t.Run("sse", func(t *testing.T) {
				home := remoteHome(t)
				installRemote(t, home, tc.hostID, ServerEntry{
					Name: "demo", Endpoint: remoteTestURL, Transport: TransportSSE,
				}, false)
				data, err := os.ReadFile(filepath.Join(home, tc.file))
				if err != nil {
					t.Fatalf("read config: %v", err)
				}
				if string(data) != tc.sse {
					t.Errorf("sse config is not the golden text\n got: %q\nwant: %q", data, tc.sse)
				}
			})
		})
	}
}

// TestRemoteEntryRoundTrip is the read-back half of Step 2.2: what the host
// will actually see re-reads as the same endpoint, no command, and a transport
// that maps back onto the registry vocabulary.
func TestRemoteEntryRoundTrip(t *testing.T) {
	for _, tc := range remoteGoldenCases() {
		t.Run(tc.hostID, func(t *testing.T) {
			home := remoteHome(t)
			installRemote(t, home, tc.hostID, ServerEntry{
				Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
			}, false)
			entries, err := ListServerEntriesWithValues(context.Background(), tc.hostID, domain.ScopeUser)
			if err != nil {
				t.Fatalf("ListServerEntriesWithValues: %v", err)
			}
			var found *HostServerEntry
			for i := range entries {
				if entries[i].Name == "demo" {
					found = &entries[i]
				}
			}
			if found == nil {
				t.Fatalf("remote entry is invisible to read-back: %+v", entries)
			}
			if found.Endpoint != remoteTestURL {
				t.Errorf("endpoint = %q, want %q", found.Endpoint, remoteTestURL)
			}
			if found.Command != "" {
				t.Errorf("remote entry grew a command: %q", found.Command)
			}
			if found.Transport != tc.readBackTransport {
				t.Errorf("host transport = %q, want %q", found.Transport, tc.readBackTransport)
			}
			if got := RegistryRemoteTransport(found.Transport); got != TransportStreamableHTTP {
				t.Errorf("registry transport = %q, want %q", got, TransportStreamableHTTP)
			}
		})
	}
}

// TestRemoteEntryRefusedWhereNeverVerified is the fail-closed matrix: a host
// with no Remote spec, and a capable host asked for a transport it does not
// document, both refuse with the typed code before a single byte is written.
func TestRemoteEntryRefusedWhereNeverVerified(t *testing.T) {
	// Tier B (partial evidence) and Tier C/D rows: no spec, no write.
	for _, hostID := range []string{"antigravity", "qwen-code", "crush", "amp", "mux", "roo", "gemini-cli"} {
		t.Run("no-spec/"+hostID, func(t *testing.T) {
			home := remoteHome(t)
			_, err := InstallServerEntry(context.Background(), hostID, ServerEntry{
				Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
			}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser})
			if err == nil {
				t.Fatalf("%s accepted a remote entry without a verified remote spec", hostID)
			}
			if !strings.Contains(err.Error(), RemoteUnsupportedCode) {
				t.Errorf("want %s, got: %v", RemoteUnsupportedCode, err)
			}
			// The capability gate runs before the config is even read: the
			// host's own config file must be exactly as it was (absent).
			adapter, aerr := GetAdapter(hostID)
			if aerr != nil {
				t.Fatalf("GetAdapter: %v", aerr)
			}
			if cfgPath, cerr := adapter.DetectConfig(context.Background(), domain.ScopeUser); cerr == nil {
				if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
					t.Errorf("refused install wrote %s (stat %v)", cfgPath, statErr)
				}
			}
		})
	}

	// Capable hosts, unsupported transport: still the typed refusal.
	for _, tc := range []struct{ hostID, transport string }{
		{"opencode", TransportSSE},
		{"codex", TransportSSE},
		{"grok-build", TransportSSE},
		{"pi-agent", TransportSSE},
		{"zed", TransportSSE},
		{"claude-code", "websocket"},
		{"cursor", "websocket"},
	} {
		t.Run("transport/"+tc.hostID+"-"+tc.transport, func(t *testing.T) {
			home := remoteHome(t)
			_, err := InstallServerEntry(context.Background(), tc.hostID, ServerEntry{
				Name: "demo", Endpoint: remoteTestURL, Transport: tc.transport,
			}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser})
			if err == nil {
				t.Fatalf("%s accepted transport %q", tc.hostID, tc.transport)
			}
			if !strings.Contains(err.Error(), RemoteUnsupportedCode) {
				t.Errorf("want %s, got: %v", RemoteUnsupportedCode, err)
			}
		})
	}
}

// TestRemoteEntryWriteGate is the one-entry-one-transport rule plus the env
// refusal: neither, both, and "--env with a URL" are all typed refusals.
func TestRemoteEntryWriteGate(t *testing.T) {
	t.Run("neither command nor endpoint", func(t *testing.T) {
		remoteHome(t)
		_, err := InstallServerEntry(context.Background(), "claude-code", ServerEntry{Name: "demo"},
			EntryInstallOptions{Scope: domain.ScopeUser})
		if err == nil || !strings.Contains(err.Error(), "no command to run") {
			t.Errorf("empty entry must be refused: %v", err)
		}
	})
	t.Run("both command and endpoint", func(t *testing.T) {
		home := remoteHome(t)
		_, err := InstallServerEntry(context.Background(), "claude-code", ServerEntry{
			Name: "demo", Command: "npx", Endpoint: remoteTestURL,
		}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser})
		if err == nil || !strings.Contains(err.Error(), "one transport") {
			t.Errorf("entry with both transports must be refused: %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(statErr) {
			t.Errorf("refused install wrote a config (stat %v)", statErr)
		}
	})
	t.Run("env with remote", func(t *testing.T) {
		home := remoteHome(t)
		_, err := InstallServerEntry(context.Background(), "claude-code", ServerEntry{
			Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
			EnvNames: []string{"API_TOKEN"},
		}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser})
		if err == nil {
			t.Fatal("--env on a remote entry must be refused, not silently dropped")
		}
		if !strings.Contains(err.Error(), "LPSM-REMOTE-ENV-REFUSED") {
			t.Errorf("want the typed env refusal, got: %v", err)
		}
		if !strings.Contains(err.Error(), "remote") {
			t.Errorf("refusal should say why: %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(statErr) {
			t.Errorf("refused install wrote a config (stat %v)", statErr)
		}
	})
}

// TestRemoteEntryNameConflictAndForce proves the collision rules did not
// change for remote entries: same name refuses, --force replaces and records
// the prior entry for a later restore.
func TestRemoteEntryNameConflictAndForce(t *testing.T) {
	home := remoteHome(t)
	ctx := context.Background()
	claudeFile := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"demo":{"command":"/usr/local/bin/my-own-server"}}}`
	if err := os.WriteFile(claudeFile, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	entry := ServerEntry{Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP}
	if _, err := InstallServerEntry(ctx, "claude-code", entry, EntryInstallOptions{
		BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser,
	}); err == nil {
		t.Fatal("remote install overwrote an existing name without --force")
	}
	if after, _ := os.ReadFile(claudeFile); string(after) != original {
		t.Errorf("a refused install must not modify the file:\n%s", after)
	}

	res, err := InstallServerEntry(ctx, "claude-code", entry, EntryInstallOptions{
		BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser, Force: true,
	})
	if err != nil {
		t.Fatalf("forced install: %v", err)
	}
	if !res.Replaced {
		t.Errorf("forced install did not report a replacement: %+v", res)
	}
	if res.PriorEntry == "" {
		t.Error("replacement recorded no prior entry to restore")
	}
	after, _ := os.ReadFile(claudeFile)
	if strings.Contains(string(after), "my-own-server") || !strings.Contains(string(after), remoteTestURL) {
		t.Errorf("forced install did not replace the entry:\n%s", after)
	}
	if strings.Contains(string(after), `"command":"npx"`) {
		t.Errorf("replacement leaked a stdio entry:\n%s", after)
	}
}

// --- Step 2.3: state / remove / restore over remote entries -------------

// remoteStateHosts are one JSON host and one TOML host, the two strip/state
// implementations.
func remoteStateHosts() []struct{ hostID, file string } {
	return []struct{ hostID, file string }{
		{"claude-code", ".claude.json"},
		{"codex", ".codex/config.toml"},
	}
}

// TestRemoteEntryRollbackRestoresPreInstallBytes proves the restore path is
// byte-exact for a remote entry: install over a config with siblings, then
// roll the write back, and the file must equal the pre-install bytes.
func TestRemoteEntryRollbackRestoresPreInstallBytes(t *testing.T) {
	for _, tc := range remoteStateHosts() {
		t.Run(tc.hostID, func(t *testing.T) {
			home := remoteHome(t)
			path := filepath.Join(home, tc.file)
			var original string
			if tc.hostID == "codex" {
				original = "# my codex settings\nmodel = \"gpt-5\"\n\n[mcp_servers.mine]\ncommand = \"npx\"\n"
			} else {
				original = "{\n  // my settings\n  \"theme\": \"One Dark\",\n" +
					"  \"mcpServers\": {\"mine\": {\"command\": \"npx\"}}\n}\n"
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}

			res, err := InstallServerEntry(context.Background(), tc.hostID, ServerEntry{
				Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
			}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser})
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if got := string(fileContents(t, path)); !strings.Contains(got, remoteTestURL) {
				t.Fatalf("install did not land:\n%s", got)
			}
			// This is the same compensation `litespm restore` replays.
			if err := RollbackConfigWrite(res.ConfigPath, res.BackupPath, res.WrittenDigest); err != nil {
				t.Fatalf("rollback: %v", err)
			}
			if got := string(fileContents(t, path)); got != original {
				t.Errorf("restore is not byte-identical\nwant: %q\ngot:  %q", original, got)
			}
		})
	}
}

// TestRemoveStripsRemoteEntryLeavingSiblings proves stripJSONEntryNamed and
// stripTOMLEntryNamed work on a remote entry: the entry goes, every sibling
// (and the file's formatting) stays byte-for-byte.
func TestRemoveStripsRemoteEntryLeavingSiblings(t *testing.T) {
	for _, tc := range remoteStateHosts() {
		t.Run(tc.hostID, func(t *testing.T) {
			home := remoteHome(t)
			path := filepath.Join(home, tc.file)
			var original string
			if tc.hostID == "codex" {
				original = "[mcp_servers.litespm]\ncommand = \"litespm\"\nargs = [\"bridge\"]\n\n" +
					"[mcp_servers.mine]\ncommand = \"npx\"\n"
			} else {
				original = "{\n  \"mcpServers\": {\n" +
					"    \"litespm\": {\"command\": \"litespm\", \"args\": [\"bridge\"]},\n" +
					"    \"mine\": {\"command\": \"npx\"}\n" +
					"  }\n}\n"
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}

			installRemote(t, home, tc.hostID, ServerEntry{
				Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
			}, false)
			if got := string(fileContents(t, path)); !strings.Contains(got, remoteTestURL) {
				t.Fatalf("install did not land:\n%s", got)
			}
			// The node's own fingerprint is observable and stable, i.e.
			// entryStateFromContent handles a remote entry.
			fp, err := EntryFingerprintNow(context.Background(), tc.hostID, "demo", domain.ScopeUser)
			if err != nil || !strings.HasPrefix(fp, "sha256:") {
				t.Fatalf("fingerprint of a remote entry: %q, %v", fp, err)
			}

			res, err := RemoveServerEntry(context.Background(), tc.hostID, "demo",
				domain.ScopeUser, filepath.Join(home, "backups"), "")
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if !res.Removed {
				t.Fatalf("remote entry was not removed: %+v", res)
			}
			if got := string(fileContents(t, path)); got != original {
				t.Errorf("removal is not byte-identical to the pre-install state\nwant: %q\ngot:  %q", original, got)
			}
		})
	}
}

// TestRemoveRemoteEntryFromCompactFileKeepsSiblings is the second remove
// shape: a hand-written single-line config where the splice cannot reproduce
// the original bytes (the member insert introduced a newline). Removal must
// still drop exactly the remote entry and nothing else — same behaviour the
// stdio path has always had for such files.
func TestRemoveRemoteEntryFromCompactFileKeepsSiblings(t *testing.T) {
	home := remoteHome(t)
	path := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"litespm":{"command":"litespm","args":["bridge"]},"mine":{"command":"npx"}}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	installRemote(t, home, "claude-code", ServerEntry{
		Name: "demo", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
	}, false)
	if _, err := RemoveServerEntry(context.Background(), "claude-code", "demo",
		domain.ScopeUser, filepath.Join(home, "backups"), ""); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after := string(fileContents(t, path))
	if strings.Contains(after, "demo") || strings.Contains(after, remoteTestURL) {
		t.Errorf("remote entry survived removal:\n%s", after)
	}
	for _, sibling := range []string{
		`"litespm":{"command":"litespm","args":["bridge"]}`,
		`"mine":{"command":"npx"}`,
	} {
		if !strings.Contains(after, sibling) {
			t.Errorf("sibling lost: %q\n%s", sibling, after)
		}
	}
	if _, perr := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(after)); perr != nil {
		t.Errorf("config invalid after removal: %v\n%s", perr, after)
	}
}

// TestRemoteEntryForceReplaceThenRemoveRestoresPrior is the prior-entry path
// for a remote entry: replace a user-authored STDIO entry with a remote one,
// remove with the recorded prior, and the original stdio entry comes back.
func TestRemoteEntryForceReplaceThenRemoveRestoresPrior(t *testing.T) {
	home := remoteHome(t)
	ctx := context.Background()
	path := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"mine":{"command":"/usr/local/bin/my-own-server"}}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallServerEntry(ctx, "claude-code", ServerEntry{
		Name: "mine", Endpoint: remoteTestURL, Transport: TransportStreamableHTTP,
	}, EntryInstallOptions{BackupDir: filepath.Join(home, "backups"), Scope: domain.ScopeUser, Force: true})
	if err != nil {
		t.Fatalf("forced install: %v", err)
	}
	rem, err := RemoveServerEntry(ctx, "claude-code", "mine", domain.ScopeUser,
		filepath.Join(home, "backups"), res.PriorEntry)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !rem.Removed || !rem.Restored {
		t.Fatalf("unexpected removal result: %+v", rem)
	}
	if got := string(fileContents(t, path)); got != original {
		t.Errorf("prior entry was not restored byte-for-byte\nwant: %q\ngot:  %q", original, got)
	}
}

// TestRemoteUnsupportedCodeIsTheSingleRefusal guards the code itself: every
// remote refusal carries it, so callers can classify instead of parsing prose.
func TestRemoteUnsupportedCodeIsTheSingleRefusal(t *testing.T) {
	if RemoteUnsupportedCode != "LPSM-HOST-REMOTE-UNSUPPORTED" {
		t.Fatalf("the typed refusal code changed: %q", RemoteUnsupportedCode)
	}
	if _, err := remoteTypeValue("nowhere", nil, TransportStreamableHTTP); err == nil ||
		!strings.Contains(err.Error(), RemoteUnsupportedCode) {
		t.Errorf("nil-spec refusal lost the code: %v", err)
	}
	// A spec that never declared the transport is refused by the same code.
	if _, err := remoteTypeValue("zed", &RemoteEntrySpec{
		URLKey: "url", Transports: []string{TransportStreamableHTTP},
	}, TransportSSE); err == nil || !strings.Contains(err.Error(), RemoteUnsupportedCode) {
		t.Errorf("transport refusal lost the code: %v", err)
	}
}

func fileContents(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
