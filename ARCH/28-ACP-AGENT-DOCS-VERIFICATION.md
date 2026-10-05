# ACP Agent Documentation Verification

This document records an independent, source-by-source verification of every agent
published in the Agent Client Protocol (ACP) registry, and the resulting
corrections to the data-driven agent adapter.

It is a **status snapshot with provenance**, not a normative contract. The
normative launch model remains "registry metadata is the source of truth"; this
document describes where the live registry metadata is *incomplete or wrong* and
how `internal/agent` compensates without inventing facts.

---

## 0. Scope and method

*   **Input:** the live ACP registry index (`https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`),
    snapshotted at `fixtures/source/acp/registry.json` (41 agents).
*   **Method:** for each agent the primary documentation site was fetched first,
    then the repository README, then a targeted web search. The documented
    install and ACP launch commands were compared against the registry
    `distribution` block (npx package + args, uvx package + args, or binary
    `cmd` + args + targets).
*   **Honesty rule:** a row is only marked *match* when a source was actually
    reached. `unreachable`/`unclear` is stated explicitly; no value is inferred.
*   **Date:** ecosystem facts verified live 2026-10-01.
*   **Not evidence of runtime acceptance:** this is documentation verification.
    No agent has been launched end-to-end on Windows in this workstream.

### Verdict vocabulary

| Verdict | Meaning |
|---|---|
| **match** | Documented package/binary and launch args agree with the registry entry. |
| **match (args)** | Distribution shape agrees and the registry already carries the correct args. |
| **unclear** | A source was reached but is ambiguous, contested, or conflicts with the registry. |
| **mismatch** | Documentation and registry disagree in a way that would break launch. |

---

## 1. Per-agent findings

"Registry args" is the `linux-x86_64` (or first) distribution's `args`. "Action"
is what `internal/agent/overrides.go` does about it.

| id | doc source verified | documented ACP launch | registry args | verdict | action |
|---|---|---|---|---|---|
| agoragentic-acp | repo README + `acp/README.md`, npm page, Zed page | `npx agoragentic-mcp@1.3.0 --acp` | `["--acp"]` | unclear | note (README calls npm package a legacy relay) |
| amp-acp | repo README, `extension.toml` | `amp-acp` (no args) | `[]` (binary) | match | — |
| antigravity-acp | Google docs, Zed page, upstream issue | no official ACP mode; standalone server | `["--uid="]` | unclear | note (undocumented standalone binary) |
| auggie | docs.augmentcode.com, repo README | `auggie --acp` | `["--acp"]` | match | — |
| autohand | repo README, vendor site | `autohand-acp` (no args) | `[]` | match | env `AUTOHAND_PERMISSION_MODE=external` + note |
| claude-acp | repo README + `package.json` | `npx @agentclientprotocol/claude-agent-acp` | `[]` | match | note (Node >= 22, auth under-documented) |
| cline | docs.cline.bot, vendor site | `cline --acp` | `["--acp"]` | match (args) | — |
| codebuddy-code | vendor docs, Zed page | `codebuddy --acp` | `["--acp"]` | match (args) | — |
| codex-acp | repo README | `npx -y @agentclientprotocol/codex-acp` | `[]` | match | — |
| cortex-code | Snowflake docs (overview, cli, acp) | `cortex acp serve -c <conn>` | `["acp","serve"]` | match (args) | note (needs `-c` + Snowflake auth) |
| corust-agent | repo `extension.toml`, releases | `./corust-agent-acp` | `[]` | match | note (repo transferred; auth undocumented) |
| crow-cli | repo README | `crow-cli acp` | `[]` (binary) | unclear | note (documented install is Python tooling) |
| cursor | cursor.com/docs/cli/acp | `agent acp` | `["acp"]` | match (args) | note (CLI binary named `agent`) |
| deepagents | npm page, docs.langchain.com | `npx deepagents-acp` | `[]` | match | note (requires `ANTHROPIC_API_KEY`) |
| devin | docs.devin.ai (cli, acp) | `devin acp` | `["acp"]` | match (args) | — |
| dimcode | vendor docs, npm page | `dim acp` | `["acp"]` | match (args) | — |
| dirac | repo README, vendor docs, Zed page | `dirac --acp` | `["--acp"]` | match (args) | — |
| factory-droid | docs.factory.ai | `droid exec --output-format acp` | `["exec","--output-format","acp-daemon"]` | unclear | note (registry uses `acp-daemon`) |
| fast-agent | fast-agent.ai, repo README | `uvx fast-agent-acp --model <m>` | `[]` (uvx) | match | — |
| gemini | geminicli.com, ACP docs | `gemini --acp` | `["--acp"]` | match (args) | note (vendor superseded some tiers) |
| github-copilot-cli | GitHub docs (ACP server) | `copilot --acp --stdio` | `["--acp"]` | **mismatch** | override args to `["--acp","--stdio"]` |
| glm-acp-agent | repo README | `glm-acp-agent` (no args) | `[]` | match | — |
| goose | goose-docs.ai | `goose acp` | `["acp"]` | match (args) | note (project moved to a foundation) |
| grok-build | x.ai/cli, npm page | `grok agent stdio` | `["agent","stdio"]` | match (args) | — |
| harn | harnlang.com (cli, acp) | `harn serve acp <file.harn>` | `["serve","acp"]` | unclear | note (needs a pipeline file) |
| junie | JetBrains docs, release repo | `junie --acp true` | `["--acp=true"]` | match (args) | — |
| kilo | kilo.ai docs, repo README | `kilo acp` | `["acp"]` | match (args) | — |
| kimchi | docs.kimchi.dev | `kimchi --mode acp` | `["--mode","acp"]` | match (args) | — |
| kimi | moonshot docs, repo README | `kimi acp` | `["acp"]` | unclear | **deprecated** (upstream archived) |
| minimax-code | vendor docs, npm page | `mcode acp` | `["acp"]` | **mismatch** | override bin to `mcode` |
| minion-code | repo README, PyPI, `pyproject.toml` | `mcode acp` | `["acp"]` (uvx) | match (args) | — |
| mistral-vibe | repo README, ACP setup docs | `vibe-acp` | `[]` | match | — |
| nova | repo README | `npx @compass-ai/nova acp` | `["acp"]` | match (args) | — |
| opencode | opencode.ai docs, repo | `opencode acp` | `["acp"]` | match (args) | — |
| pi-acp | repo README | `npx -y pi-acp` | `[]` | match | — |
| poolside | repo README | `pool acp` | `["acp"]` | match (args) | — |
| qoder | docs.qoder.com, Zed page | `qoder --acp` (docs) / `acp` (Zed) | `["--acp"]` | unclear | — (registry already `--acp`) |
| qwen-code | repo README, Zed guide | `qwen --acp` | `["--acp","--experimental-skills"]` | match (args) | — |
| sigit | repo README, npm page | `sigit --acp` | `[]` | **mismatch** | override args to `["--acp"]` |
| stakpak | repo README | `stakpak acp` | `["acp"]` | match (args) | — |
| vtcode | repo ACP guide | `vtcode acp` | `["acp"]` | match (args) | — |

**Tally:** 41 agents; 3 launch-affecting defects corrected (sigit, minimax-code,
github-copilot-cli), 1 deprecation (kimi), and 13 rows that carry an
informational note only (§2.4). 13 + 3 + 1 = **17** entries in `launchOverrides`;
the remaining 24 rows needed no action. No package or binary name was wrong in a
way that required remapping beyond `minimax-code`.

---

## 2. Corrections applied (`internal/agent/overrides.go`)

The registry is discovery-truth; the override layer is a small, documented,
per-id patch consulted at the top of `Resolve` and applied while the
`LaunchSpec` is built (`internal/agent/acp.go:74, 81-133, 148-151`). It changes
only three launches and attaches notes otherwise.

### 2.1 Launch-affecting

| id | Change | Why |
|---|---|---|
| `sigit` | args → `["--acp"]` | Registry ships no args; docs use the `--acp` flag. Without it the CLI does not enter ACP. |
| `minimax-code` | resolve via `npx -p @minimax-ai/code <ver> mcode acp` | Package bin is `mcode`, not the package id; a bare `npx <pkg> acp` cannot resolve the bin. |
| `github-copilot-cli` | args → `["--acp","--stdio"]` | Bare `--acp` selects the TCP/UI mode; the stdio server needs `--stdio`. |

### 2.2 Environment injection

| id | Env | Why |
|---|---|---|
| `autohand` | `AUTOHAND_PERMISSION_MODE=external` | Documented mode for forwarding permission prompts to the host. |

### 2.3 Deprecation

| id | Status | Why |
|---|---|---|
| `kimi` | deprecated | The registry points at an archived project; the replacement is a different product/package. Marked deprecated in the override layer and in catalog ingestion (`domain.ListingStatusDeprecated`), so it is filtered rather than silently installed. |

### 2.4 Informational notes (no launch change)

`agoragentic-acp`, `antigravity-acp`, `autohand`, `claude-acp`, `corust-agent`,
`cortex-code`, `crow-cli`, `cursor`, `deepagents`, `factory-droid`, `gemini`,
`goose`, `harn`. These are surfaced by `litespm agent resolve` as `Note:` lines.

---

## 3. Corrections deliberately *not* applied

| id | Candidate change | Why deferred |
|---|---|---|
| `cortex-code` | append `-c <connection>` | The connection name is user-specific and cannot be synthesized. Requires a runtime/config value; noted, not defaulted. |
| `harn` | append a pipeline path | The pipeline file is user-supplied. Requires a runtime value; noted. |
| `factory-droid` | `acp` vs `acp-daemon` | Docs and registry disagree and no source confirms which the pinned version accepts. Left as registry (`acp-daemon`) with a note. |
| `cursor` | binary name `agent` vs `cursor-agent` | The registry path (`./dist-package/cursor-agent`) is archive-internal and may be correct; no reliable way to verify without downloading. Noted. |
| `antigravity-acp` | official `agy` mapping | No official ACP mode exists; the registry entry is a third-party server. Noted, not "corrected" into something unverified. |
| `qoder` | `--acp` vs `acp` | Docs conflict; registry already carries `--acp`. Left as registry. |
| `crow-cli`, `corust-agent`, `agoragentic-acp` | package/binary remaps | Sources are contradictory or the repo is transferred; remapping would be a guess. Noted. |

---

## 4. Binary digest coverage

Binary distributions do **not** all carry a `sha256`. The adapter records the
digest when present and an empty digest otherwise; it never fabricates one.

Measured against `fixtures/source/acp/registry.json`: **19** of the 41 agents
have a `binary` distribution at all (the other 22 launch via `npx`/`uvx` and have
no digest to carry).

*   **With digest (10):** `amp-acp`, `goose`, `harn`, `kilo`, `kimchi`, `kimi`, `mistral-vibe`, `opencode`, `poolside`, `sigit`.
*   **Without digest (9):** `antigravity-acp`, `cortex-code`, `corust-agent`, `crow-cli`, `cursor`, `devin`, `junie`, `stakpak`, `vtcode`.

`agent install` — **`DESIGNED`; the subcommand does not exist.**
`runAgentCommand` accepts only `list` (`cmd/litespm/main.go:615`) and `resolve`
(`main.go:641`), and its usage string says `litespm agent [list|resolve <id> …]`
(`main.go:566`) — there is no dispatcher path for `agent install` at all. When it
is built, it must treat the digest as *optional-but-verified-when-present*, and
must refuse or warn when a binary has no digest. This is a known gap, not an
acceptance result.

---

## 5. Consequences for the adapter

1.  `Resolve` (`internal/agent/acp.go:72-154`) consults `overrideFor` before
    building each strategy's `LaunchSpec` — `Args`, `Executable`/`NpxBin` and
    `Env` replace the registry values in place (`acp.go:81-133`) — then attaches
    `Notes` and `Deprecated` at the end (`acp.go:148-151`).
2.  `litespm agent list` marks deprecated agents (`cmd/litespm/main.go:628-637`);
    `litespm agent resolve` prints `Warning:` for deprecation and one `Note:`
    line per note (`main.go:684-687`).
3.  Catalog ingestion maps deprecated agents to `domain.ListingStatusDeprecated`
    (`internal/source/acp_registry.go:89-92`). State: `internal/source` is
    `IMPLEMENTED` with **test-only callers** (`STATUS.md` §2) — this is code that
    exists, not a publish path that runs.
4.  Tests in `internal/agent/agent_test.go:64` (`TestOverridesAgainstRegistry`)
    assert the three launch corrections, the deprecation flag, and that an
    already-correct registry entry (`cline`) is left untouched.

---

## 6. Remaining verification work

*   **Runtime launch acceptance** for each strategy (npx / uvx / binary) on
    Windows — not done.
*   **Binary download + digest verification** (`agent install`) — `DESIGNED`;
    the subcommand is absent from the dispatcher (§4).
*   **Auth-state detection** (which agents need a key vs. interactive login) —
    documented per agent above, not yet wired into readiness.
*   **Config-directory detection** — documented for several agents, not yet
    recorded in the domain model.
