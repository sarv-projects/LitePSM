# ARCH-30 — Data-Driven Agent Bridge Targets

Status: **`WIRED`.** The 44-row table is compiled into the adapter registry (`internal/host/registry.go:34-41`) and drives `host detect/setup` through `GenericAdapter`; `litespm host list` reports 50 adapters (6 bespoke + 44 generic).
Related: [16 — Host Adapters](16-HOST-ADAPTERS.md), [14 — Bridge & Provider MCP](14-BRIDGE-PROVIDER-MCP.md), [29 — Connector System Design](29-CONNECTOR-SYSTEM-DESIGN.md).

---

## 1. Problem

Six agent hosts were supported by six hand-written `HostAdapter` implementations (`internal/host/{claudecode,cline,codex,opencode,piagent,grokbuild}.go`). Each is roughly 170–290 lines, and the overwhelming majority is identical: resolve a path, back up the file, merge one `litespm` entry, verify.

Two problems with that shape:

1. **It does not scale.** Covering the agent ecosystem at that ratio is tens of thousands of lines of duplicated boilerplate.
2. **The knowledge has no evidence trail.** Which config path, which key, which entry shape — all of it is buried in code. A reviewer cannot tell a verified path from an assumed one, and an assumed path produces an installer that silently writes to the wrong place. That is the worst possible failure for a tool whose job is editing other people's config files.

## 2. Decision

Split the adapter into the part that is generic and the part that is data.

* **`BridgeTarget`** (`internal/host/target.go`) — one agent's MCP configuration as a struct: config path per scope, format, MCP key path, entry shape, detection signals, the documentation URL it was verified against, and caveats.
* **`GenericAdapter`** (`internal/host/generic.go`) — the only implementation of the `HostAdapter` contract. It carries no per-agent knowledge.
* **`verifiedBridgeTargets`** (`internal/host/targets_data.go`) — the table. Adding an agent is one row plus a test.

The six hand-written adapters are **retained unchanged**. They predate this design, are individually tested, and carry behaviour the generic path does not model (OpenCode's dual v1/v2 layouts, Codex and Grok's TOML table handling, Pi's extension generation, Cline's comment preservation). `TestBridgeTargetTableDoesNotShadowBespokeAdapters` keeps the two generations from overlapping.

## 3. Honesty contract

A row ships only if its config path, MCP key and entry shape were read from that agent's own documentation or repository. `DocsURL` records the source and is required — enforced by `TestBridgeTargetRowsAreComplete`.

Agents that could not be verified are **absent from the table**, with the reason recorded in a comment block above it so the gap is not silently re-filled later. The list below names **24 agents**, but one of them (`roo`) is excluded only at *user scope* — its repo-scope target **is** shipped — so the number of agents with **no** shipped bridge target is **23** (see §10). Current exclusions and their causes:

| Excluded | Reason |
|---|---|
| `windsurf` | Current docs point at `~/.config/devin/mcp_config.json` — the same file the `devin` row owns. Two hosts silently sharing one file means setting up either rewrites the other's config. |
| `continue`, `goose`, `hermes-agent` | YAML configs; `goose` additionally uses key `extensions` and `cmd` instead of `command`, and does not support SSE. |
| `mistral-vibe`, `vtcode`, `reasonix`, `autohand-code` | Array-shaped server maps (`[[mcp_servers]]`, `[[mcp.providers]]`, `[[plugins]]`) or multi-format configs. Each needs its own reader. |
| `dexto`, `eve` | MCP config lives inside the agent's own YAML / TypeScript source, not a config file. |
| `lingma`, `loaf`, `tinycloud`, `moxby`, `mcpjam`, `replit` | No documented on-disk client config, or no vendor source could be reached. |
| `sarvam-code` | Product unreleased. |
| `terramind` | Not an agent: IBM's Earth Observation foundation model. |
| `promptscript` | MCP is an unimplemented roadmap item. |
| `adal`, `trae`, `trae-cn` | MCP works, but the user-scope config path is not published. |
| `roo` (user scope) | Stored in the IDE's `globalStorage`, whose absolute path the vendor docs never print. Repo scope is shipped instead. |
| `codemaker` | No reachable vendor documentation at all. |

## 4. Config writing is surgical

Parsing a config into `map[string]any` and re-serializing it would delete every `//` comment in it. Several verified hosts keep user-authored comments in their config, and one (`tabnine-cli`) keeps `mcpServers` inside a large shared settings document used by unrelated features. Silent comment deletion is data loss in an installer.

So `mergeJSONEntrySurgical` (`internal/host/jsonc_merge.go`) locates the target object **by byte offset** and splices only the `litespm` member into it. Everything else survives byte-for-byte: comments, key order, indentation, blank lines.

The mechanism rests on one invariant, pinned by `TestStripJSONCommentsPreservesOffsets`: `stripJSONComments` replaces comments with equal-length whitespace, so offsets resolved against the stripped text are valid offsets into the original.

Two consequences, both deliberate:

* **Strict-JSON hosts refuse a commented file** rather than guessing. `TestStrictJSONHostsRefuseCommentedFiles` pins both halves of this policy so they cannot drift into each other.
* **Every merge is re-parsed and asserted** before anything is written. A writer bug returns an error; it never ships a corrupt config file.

TOML targets use `mergeTOMLEntry`, which replaces or appends a single `[mcp_servers.litespm]` table and leaves every other section — including comments — untouched.

## 5. Supported shapes

| Shape | Written as | Hosts |
|---|---|---|
| `ShapeObject` | `{"command": …, "args": [...]}` | the majority |
| `ShapeLocalArray` | `{"type": "local", "command": [argv…]}` | opencode, kilo, fx, codearts-agent, posit-assistant |
| `ShapeCommandString` | `"<bin> bridge stdio --host <id>"` | mux (Xum) |

MCP key paths in use: `mcpServers`, nested `mcp` and `amp.mcpServers`, `mcp.servers`, `context_servers` (Zed), `servers` (codestudio, Xum).

## 6. Path resolution

`resolveHomeDir()` honours `USERPROFILE` on Windows and `HOME` on Unix. `%APPDATA%` is read **only** when `runtime.GOOS == "windows"` — gating on the variable merely being set silently redirected config paths during testing, since `APPDATA` is meaningless on Unix. `XDG_CONFIG_HOME` is honoured via `xdgConfigDir`.

Per-target overrides actually read by this resolver (`internal/host/targets_data.go`): `XDG_CONFIG_HOME`
(via `xdgConfigDir`), `ASTRBOT_ROOT`, `GEMINI_CLI_HOME`, `COPILOT_HOME`, `KIMI_CODE_HOME`,
`QODER_CONFIG_DIR`, `QODERCN_CONFIG_DIR`, `KODE_CONFIG_DIR`, `OH_PERSISTENCE_DIR`,
`CRUSH_GLOBAL_CONFIG`.

Agent-home variables `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `GROK_HOME`, `AUTOHAND_HOME`,
`HERMES_HOME`, `VIBE_HOME` and `SARVAM_HOME` are honoured by **`internal/skills`** only — they
select where skill directories are written, not where a host's config file lives; no host adapter
reads them (`ARCH/16` §3.4–§3.5). `AIDER_DESK_HOME_DIR` is not read anywhere in the tree.

## 7. Integrity tests

These exist to make an unverified or wrong row impossible to ship quietly:

| Test | Prevents |
|---|---|
| `TestBridgeTargetRowsAreComplete` | A row without a `DocsURL`, a key path, or a detection signal. |
| `TestBridgeTargetTableHasNoUndeclaredSharedConfigFiles` | Two hosts resolving to one config file unless **both** declare the overlap in `SharedConfigWith` and explain it in `Note`. This is the Windsurf bug class. Currently one declared pair: `antigravity` / `antigravity-cli`, which genuinely share `~/.gemini/config/mcp_config.json`. |
| `TestBridgeTargetTableDoesNotShadowBespokeAdapters` | A table row silently replacing a tested hand-written adapter. |
| `TestBridgeTargetTableExcludesUnverifiedAgents` | Quietly re-adding an agent that was excluded for lack of evidence. |
| `TestEveryTableTargetAppliesToARealisticFile` | Any row emitting a broken edit — runs all 44 rows against a realistic seeded config and asserts the bridge entry lands, the key path is right, and foreign servers and unrelated settings survive. |
| `TestGenericAdapterPreservesUserCommentsEndToEnd` | Regression test for the comment-deletion data-loss bug. |
| `TestRenderConfigRefusesToClobberScalar` | Confused-deputy overwrite: if a key path collides with an unrelated scalar, fail rather than replace it. |

## 8. Verification

Run at time of writing:

```
go build ./...
go vet ./...
go test ./... -count=1     # all packages green
```

Registered adapters after this change: **50** (6 hand-written + 44 verified targets).

### 8.1 Host documentation audit — 2026-10-06

Every row in `internal/host/targets_data.go` and all six hand-written adapters
were re-checked against live vendor documentation, one host at a time. The
previous claim in that file's header — "every row here was checked against that
agent's own documentation" — was not true for every row, and the audit found
defects that produce **silent failure**: LiteSPM writes the bridge, reports
`ready`, and the host never reads it.

**Fixed (silent failure — the host ignored us):**

| Row / adapter | Defect |
|---|---|
| `opencode` | Wrote `mcp.servers.litespm`. No such key exists: the published schema types `mcp` as a map of server entries and the runtime rejects a member without `type`. Also probed `~/.opencode.json` and `%APPDATA%\OpenCode\opencode.json` (no release reads either), ignored `OPENCODE_CONFIG_DIR`/`XDG_CONFIG_HOME`, and never looked at `opencode.jsonc`. |
| `cline` | Wrote the VS Code `globalStorage` settings file. Cline moved it to `~/.cline/data/settings/cline_mcp_settings.json` (shared by all clients) and reads globalStorage only in a one-shot migration, so writes there are a no-op. `CLINE_MCP_SETTINGS_PATH`/`CLINE_DATA_DIR` ignored. |
| `amp` | Wrote a nested `{"amp":{"mcpServers":…}}`. Amp's published schema declares the property literally as `"amp.mcpServers"` with `additionalProperties:false`, so the nested form was both unread and schema-invalid. Now `FlatKey`. |
| `pi-agent` | Probed two undocumented paths, and switched to a `mcp`/`mcp.servers` container whenever the file contained an unrelated top-level `mcp` object — a container no Pi version reads. `PI_CODING_AGENT_DIR` ignored. |
| `crush` | Windows user-global path taken from `%APPDATA%`; Crush reads `%LOCALAPPDATA%`. Its schema marks `type` as **required** and its decoder applies no default, so the entry started no transport at all. New `ShapeStdioTyped`. |
| `codex` / `grok-build` | Ignored `CODEX_HOME` / `GROK_HOME` (both documented; `internal/skills` already honoured them, so the two subsystems disagreed). Probed an undocumented `%APPDATA%` fallback. |
| `codebuddy` | Candidate list skipped the documented middle entry (`~/.codebuddy/mcp.json`, `<root>/mcp.json`), so an existing one made us write to a file the host never consults. Its own `Note` already documented the correct chain. |
| `kode` | `KODE_CONFIG_DIR` changes the **filename** to `config.json`, not `kode.json`. |
| `kilo` | Selected `kilo.jsonc` without `TolerateComments`, so setup failed on the very comments that motivate `.jsonc`. |
| `fx` | Project `.mcp.json` uses `mcpServers`, not `mcp`. |
| `aider-desk` | `AIDER_DESK_HOME_DIR` ignored. `qwen-code`: `QWEN_HOME` ignored. |
| `astrbot` | Fell back to `$HOME` for its root; AstrBot falls back to the process working directory. |
| `tabnine-cli` | Documented workspace scope unregistered; published `DocsURL` returns 404. |
| `cortex`, `codearts-agent`, `kiro-cli` | Documented candidate/scope paths missing. |
| shared writer | `RenderManualSetup` emitted the entry object where the **server name** belongs, so the wizard's paste-this-snippet fallback printed invalid JSON for all 44 rows. |

**Open, deliberately not "fixed":**

* **Vendor self-contradictions on `type`.** Cursor and Firebender publish a field table marking `type` required while every JSON example on the page omits it; the Snowflake CLI page requires it and its Desktop page omits it; Kiro, Copilot, Augment and Rovo are unverifiable either way. We write the minimal stdio entry and do not guess. If any of these turns out to require `type`, the row's shape is a one-line change.
* **Trailing commas.** CodeBuddy documents JSONC *with* trailing commas. Go's decoder rejects them, so such a file fails closed at setup. Widening the reader is a separate, larger change than comment tolerance.
* **Comment tolerance can outpace the host.** Cline, Claude Code, Pi and Roo parse with a strict `JSON.parse`. Our tolerant reader will splice into a commented file that the host then rejects. Fail-closed on our side is not enough to surface it; see §9.
* **Unverified row details** left in place but not relied upon: the `OH_PERSISTENCE_DIR` branch, droid's "user wins" precedence note, and the Devin note's Windsurf claim (vendor pages 404).

The durable lesson, now enforced by `internal/host/host_docs_audit_test.go`:
assert the *host-visible* result (valid JSON, the server named, the entry under
the key the vendor documents), not just that our own writer round-trips.

## 9. Known limitations

* `TolerateComments` is advisory metadata. The writer preserves comments for all JSON targets regardless; the flag records which hosts document the format as comment-tolerant.
* `DetectPaths` and `DetectBinaries` are recorded but install-detection is not yet wired into a CLI command; `DetectInstalledHosts` currently reports verification state only.
* Several rows note that a **Windows-native path is derived from `~` expansion** rather than stated by the vendor. `resolveHomeDir` applies the standard expansion, which is correct but is an inference, not a quotation.
* No target supports TOML array-of-tables or YAML. Those two formats account for the majority of the 23 fully-excluded agents (the §3 table names 24 agents; `roo` is a scope-only exclusion, shipped at repo scope).

## 10. Relationship to the 77 skill targets

Two different registries, deliberately not conflated:

* **Bridge targets** (this document, 44 rows) — agents whose **config file LiteSPM can edit** to register the MCP bridge.
* **Skill install targets** (`internal/skills/agents.go`, 77 entries) — agents where LiteSPM can **write a `SKILL.md`** into a skills directory.

The sets overlap but are not equal. 67 agents were researched for bridge support: **44 shipped** and **23 fully excluded**. (The §3 exclusion table names 24 agents, but `roo` is excluded only at user scope; its repo-scope target is shipped, so it is counted among the 44, not among the 23.) Some skill targets are IDE extensions with no CLI config file; some bridge targets are cloud products with no skills directory. `litespm host list` reports bridge adapters only — it does not claim skill coverage.
