<div align="center">

# litespm

**One control plane for every AI coding agent capability.**

Discover and manage MCP servers, Agent Skills, and plugins
across Claude Code, OpenAI Codex, OpenCode, Cline, and many more.

[![npm version](https://img.shields.io/npm/v/litespm.svg?color=10b981)](https://www.npmjs.com/package/litespm)
[![npm downloads](https://img.shields.io/npm/dm/litespm.svg?color=10b981)](https://www.npmjs.com/package/litespm)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](https://github.com/sarv-projects/LiteSPM/blob/main/LICENSE)
[![node](https://img.shields.io/badge/node-%3E%3D18-brightgreen.svg)](https://nodejs.org)

</div>

---

```console
$ litespm
● Checking adapter advisory metadata...
  ✓ Protocol Version: 2026-07-28
  ✓ 6 Verified Host Adapters Compiled & Available

Select your primary AI Agent Host:
  [1] Cline          (VS Code Extension - cline_mcp_settings.json)
  [4] Claude Code    (Terminal CLI (claude) - ~/.claude.json (JSON))
  ...                                          [q] Quit
Enter choice [1-6, q]: 4

Scanning filesystem for Claude Code configuration...
✓ Detected configuration file: /home/you/.claude.json

Generating integration plan for Claude Code...
● Performing safe atomic configuration merge...
✓ Integration successfully applied!
  • Backup created: /home/you/.claude.json.bak-…
  • Config updated: /home/you/.claude.json
```

Setup prints **no capability count**: the wizard performs no network fetch —
advisories are compiled into the binary (`cmd/litespm/wizard.go:138-143`).
`/marketplace` becomes available in the agent after the bridge entry lands.

Stop hand-editing `~/.claude.json`, `config.toml`, and a dozen other files.
Connect an agent **once**, then manage all of its capabilities from one local
place.

## Install

```bash
npm install -g litespm
```

Prefer a standalone binary? Grab the release for your platform from
[GitHub Releases](https://github.com/sarv-projects/LiteSPM/releases). The npm
package is a thin launcher: it downloads the native binary for your OS and
checks it against the published SHA-256 checksums — aborting on a mismatch, but
**warning and proceeding unverified** if the manifest has no entry for your
platform (see [`SECURITY.md`](https://github.com/sarv-projects/LiteSPM/blob/main/SECURITY.md)).

> **Honest status.** The catalog is live end-to-end and skills install from it
> (`litespm install <skill-id>` fetches the source and writes the skill files).
> MCP and plugin installs are not end-to-end: the published catalog carries no
> artifact locator for them, so they fail closed. See
> [`STATUS.md`](https://github.com/sarv-projects/LiteSPM/blob/main/STATUS.md).

## Quickstart

```bash
litespm                    # interactive setup wizard
litespm search postgres    # search the federated catalog
litespm install <id>       # install a capability (skills + MCP servers; plugins pending an artifact source)
litespm host list          # show supported agents
litespm doctor             # run local health checks
litespm self-update        # update the binary
```

Inside a configured agent, just type:

```text
/marketplace
```

## Why litespm

- **One bridge, zero config drift.** Each agent gets a single version-pinned
  `litespm` entry. The host adapters perform an MCP-entry merge only; they do not
  write instructions, skills, or command files.
- **Secrets never leave your machine.** Downstream API keys and OAuth tokens
  live in your OS vault (Windows DPAPI-protected master key, macOS Keychain,
  Linux Secret Service). Nothing is proxied through a cloud.
- **Hardened extraction.** Archive handling rejects path traversal, absolute
  paths, symlinks, device files, case-fold collisions, and oversize payloads,
  and unpacks into a content-addressed store.
- **Fail-closed by design.** Effectful actions are intended to require explicit,
  cryptographically bound approval. Skills and MCP servers install for real (skills
  through the ledger, MCP servers through host-config registration, both gated by a
  plan plus a human approval); plugin installs still fail closed with
  `LPSM-ARTIFACT-UNAVAILABLE`, and `get_invocation` / `cancel_invocation` (`-32601`)
  have no invocation registry behind them —
  the wiring is tracked in [`STATUS.md`](https://github.com/sarv-projects/LiteSPM/blob/main/STATUS.md).
- **Drift-bound approvals** (`IMPLEMENTED`, not yet wired). Approvals are designed
  to bind to a tool's schema fingerprint and content digest, so a change forces
  re-confirmation; the grant store is not yet reachable from a user workflow.

## Supported hosts

| Agent | Interface | Config | Windows | macOS | Linux |
|---|---|:--:|:--:|:--:|:--:|
| **Claude Code** | CLI | `~/.claude.json` | ✓ | ✓ | ✓ |
| **OpenAI Codex** | CLI | `~/.codex/config.toml` | ✓ | ✓ | ✓ |
| **OpenCode** | CLI | `opencode.json` | ✓ | ✓ | ✓ |
| **Cline** | VS Code | `cline_mcp_settings.json` | ✓ | ✓ | ✓ |
| **Pi Agent** | Terminal | `~/.pi/agent/mcp.json` | ✓ | ✓ | ✓ |
| **Grok Build** | Terminal | `~/.grok/config.toml` | ✓ | ✓ | ✓ |

## Commands

| Command | What it does |
|---|---|
| `litespm` / `litespm setup` / `init` | Interactive agent selection and setup (no non-interactive form) |
| `litespm search <query>` | Search MCP servers, skills, and plugins |
| `litespm install <id>` | Install a capability (skills and MCP servers complete; plugins pending an artifact source) |
| `litespm uninstall [--dry-run]` | Remove the bridge entry from every agent host config |
| `litespm bridge stdio --host <id>` | MCP stdio bridge used by hosts |
| `litespm host [list\|detect\|setup\|remove]` | Inspect and configure host adapters |
| `litespm agent [list\|resolve <id>]` | List ACP agents / resolve a launch spec |
| `litespm skills [add\|list\|update\|remove]` | Manage installed `SKILL.md` skills |
| `litespm doctor [--repair] [--yes]` | Health checks and repairs |
| `litespm catalog sync` | Refresh the local catalog cache from the published release |
| `litespm daemon serve` | Start the background supervisor and IPC engine |
| `litespm self-update [--force]` | Update the native binary |

## How it works

```text
  Claude · Codex · OpenCode · Cline · Pi · Grok
                     │  stdio (MCP)
                     ▼
             litespm bridge shim
                     │  local IPC (OS-level ACLs)
                     ▼
              litespm daemon  ──  SQLite (WAL) · policy · secret vault
                     │
                     ▼
        MCP servers · skills · plugins
```

The bridge shim is stateless and never touches your config or database
directly. A single local daemon owns all state and supervises provider
processes. Injecting stored secrets into a provider's environment at launch is
`IMPLEMENTED`, not `WIRED` yet (no production caller — see
[`STATUS.md`](https://github.com/sarv-projects/LiteSPM/blob/main/STATUS.md) §1).

## Security

- Local IPC restricted to your OS user (Named Pipe DACL / Unix socket `0600`).
- Credentials stored only in the OS vault — never in the database or logs.
- Archive extraction is bounded and rejects traversal, absolute paths,
  symlinks, device files, and case-fold collisions.
- Capability approvals are designed to bind to `(capability, schema fingerprint,
  content digest)`, forcing re-approval on upstream changes — `IMPLEMENTED`, not
  yet reachable from a workflow (see the status note above).

### Installing a local build

`npm install` installs the published binary for this package version. If a
checkout has a stale `dist/` binary it is ignored, deliberately: the postinstall
used to prefer it and stamp the install `"local"`, so the stale build was
reinstalled forever and the pinned release never arrived.

To install a binary you built yourself (for example while testing a change
before publishing a release), opt in explicitly:

```bash
LITESPM_ALLOW_LOCAL_DIST=1 npm install -g litespm
```

That path does not verify a checksum, because a local build has no published
digest.

## Requirements

- Node.js ≥ 18 (for the npm launcher), or a supported native binary.
- Windows 10+, macOS 12+, or a modern Linux distribution.

## Links

- **Repository:** https://github.com/sarv-projects/LiteSPM
- **Issues:** https://github.com/sarv-projects/LiteSPM/issues
- **Catalog API:** https://litespm.sarveshbh-2022.workers.dev/v1/current.json

## License

[Apache-2.0](https://github.com/sarv-projects/LiteSPM/blob/main/LICENSE)
