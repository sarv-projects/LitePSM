# ARCH-30 — Data-Driven Agent Bridge Targets

Status: **implemented.**
Related: [16 — Host Adapters](16-HOST-ADAPTERS.md), [14 — Bridge & Provider MCP](14-BRIDGE-PROVIDER-MCP.md), [29 — Connector System Design](29-CONNECTOR-SYSTEM-DESIGN.md).

---

## 1. Problem

Six agent hosts were supported by six hand-written `HostAdapter` implementations (`internal/host/{claudecode,cline,codex,opencode,piagent,grokbuild}.go`). Each is roughly 170–290 lines, and the overwhelming majority is identical: resolve a path, back up the file, merge one `litepsm` entry, verify.

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

Agents that could not be verified are **absent from the table**, with the reason recorded in a comment block above it so the gap is not silently re-filled later. Current exclusions and their causes:

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

So `mergeJSONEntrySurgical` (`internal/host/jsonc_merge.go`) locates the target object **by byte offset** and splices only the `litepsm` member into it. Everything else survives byte-for-byte: comments, key order, indentation, blank lines.

The mechanism rests on one invariant, pinned by `TestStripJSONCommentsPreservesOffsets`: `stripJSONComments` replaces comments with equal-length whitespace, so offsets resolved against the stripped text are valid offsets into the original.

Two consequences, both deliberate:

* **Strict-JSON hosts refuse a commented file** rather than guessing. `TestStrictJSONHostsRefuseCommentedFiles` pins both halves of this policy so they cannot drift into each other.
* **Every merge is re-parsed and asserted** before anything is written. A writer bug returns an error; it never ships a corrupt config file.

TOML targets use `mergeTOMLEntry`, which replaces or appends a single `[mcp_servers.litepsm]` table and leaves every other section — including comments — untouched.

## 5. Supported shapes

| Shape | Written as | Hosts |
|---|---|---|
| `ShapeObject` | `{"command": …, "args": [...]}` | the majority |
| `ShapeLocalArray` | `{"type": "local", "command": [argv…]}` | opencode, kilo, fx, codearts-agent, posit-assistant |
| `ShapeCommandString` | `"<bin> bridge stdio --host <id>"` | mux (Xum) |

MCP key paths in use: `mcpServers`, nested `mcp` and `amp.mcpServers`, `mcp.servers`, `context_servers` (Zed), `servers` (codestudio, Xum).

## 6. Path resolution

`resolveHomeDir()` honours `USERPROFILE` on Windows and `HOME` on Unix. `%APPDATA%` is read **only** when `runtime.GOOS == "windows"` — gating on the variable merely being set silently redirected config paths during testing, since `APPDATA` is meaningless on Unix. `XDG_CONFIG_HOME` is honoured via `xdgConfigDir`.

Per-agent overrides implemented where documented: `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `GROK_HOME`, `XDG_CONFIG_HOME`, `AUTOHAND_HOME`, `HERMES_HOME`, `VIBE_HOME`, `SARVAM_HOME`, `GEMINI_CLI_HOME`, `COPILOT_HOME`, `KIMI_CODE_HOME`, `QODER_CONFIG_DIR`, `QODERCN_CONFIG_DIR`, `KODE_CONFIG_DIR`, `AIDER_DESK_HOME_DIR`, `ASTRBOT_ROOT`, `OH_PERSISTENCE_DIR`, `CRUSH_GLOBAL_CONFIG`.

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

## 9. Known limitations

* `TolerateComments` is advisory metadata. The writer preserves comments for all JSON targets regardless; the flag records which hosts document the format as comment-tolerant.
* `DetectPaths` and `DetectBinaries` are recorded but install-detection is not yet wired into a CLI command; `DetectInstalledHosts` currently reports verification state only.
* Several rows note that a **Windows-native path is derived from `~` expansion** rather than stated by the vendor. `resolveHomeDir` applies the standard expansion, which is correct but is an inference, not a quotation.
* No target supports TOML array-of-tables or YAML. Those two formats account for the majority of the 23 exclusions.

## 10. Relationship to the 77 skill targets

Two different registries, deliberately not conflated:

* **Bridge targets** (this document, 44 rows) — agents whose **config file LitePSM can edit** to register the MCP bridge.
* **Skill install targets** (`internal/skills/agents.go`, 77 entries) — agents where LitePSM can **write a `SKILL.md`** into a skills directory.

The sets overlap but are not equal. 67 agents were researched for bridge support (44 shipped, 23 excluded). Some skill targets are IDE extensions with no CLI config file; some bridge targets are cloud products with no skills directory. `litepsm host list` reports bridge adapters only — it does not claim skill coverage.
