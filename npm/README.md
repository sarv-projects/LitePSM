<div align="center">

# litepsm

**One control plane for every AI coding agent capability.**

Discover · install · verify · supervise — MCP servers, Agent Skills, and plugins,
across Claude Code, OpenAI Codex, OpenCode, Cline, and many more.

[![npm version](https://img.shields.io/npm/v/litepsm.svg?color=10b981)](https://www.npmjs.com/package/marketplace)
[![npm downloads](https://img.shields.io/npm/dm/litepsm.svg?color=10b981)](https://www.npmjs.com/package/marketplace)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](https://github.com/sarv-projects/LitePSM/blob/main/LICENSE)
[![node](https://img.shields.io/badge/node-%3E%3D18-brightgreen.svg)](https://nodejs.org)

</div>

---

```console
$ litepsm
  ✓ 5,185 capabilities indexed · 6 hosts detected
  ? Select your AI agent  › Claude Code
  ✓ Bridge registered in ~/.claude.json (backup saved)
  ✓ Ready — type /marketplace inside Claude Code
```

Stop hand-editing `~/.claude.json`, `config.toml`, and a dozen other files.
Connect an agent **once**, then manage all of its capabilities from one local
place.

## Install

```bash
npm install -g litepsm
```

Prefer a standalone binary? Grab the release for your platform from
[GitHub Releases](https://github.com/sarv-projects/LitePSM/releases). The npm
package is a thin launcher: it resolves the native binary for your OS, verifies
its published SHA-256 checksum, then runs it.

## Quickstart

```bash
litepsm                    # interactive setup wizard
litepsm search postgres    # search the federated catalog
litepsm install <id>       # resolve, verify, and install
litepsm host list          # show supported agents
litepsm doctor             # run local health checks
litepsm self-update        # update the binary
```

Inside a configured agent, just type:

```text
/marketplace
```

## Why litepsm

- **One bridge, zero config drift.** Each agent gets a single version-pinned
  `litepsm` entry. Add, update, or remove capabilities without touching host
  files again.
- **Secrets never leave your machine.** Downstream API keys and OAuth tokens
  live in your OS vault (Windows Credential Manager / DPAPI, macOS Keychain,
  Linux Secret Service). Nothing is proxied through a cloud.
- **Verified, not vibes.** Every artifact is checksum-verified and unpacked into
  a content-addressed store, with hard limits against path traversal, zip
  bombs, symlink escapes, and case collisions.
- **Nothing runs without you.** Effectful actions are fail-closed: a model
  cannot install or invoke anything without explicit, cryptographically bound
  approval.
- **Drift-proof approvals.** Approvals bind to a tool's schema fingerprint and
  its content digest. If either changes, access is revoked and re-confirmed.

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
| `litepsm` | Interactive agent selection and setup |
| `litepsm setup <agent>` | Configure a specific host non-interactively |
| `litepsm search <query>` | Search MCP servers, skills, and plugins |
| `litepsm install <id>` | Resolve and install a capability |
| `litepsm bridge stdio --host <id>` | MCP stdio bridge used by hosts |
| `litepsm host [list\|detect\|setup]` | Inspect and configure host adapters |
| `litepsm doctor [--repair]` | Health checks and repairs |
| `litepsm catalog sync` | Refresh the local catalog cache |
| `litepsm self-update` | Update the native binary |

## How it works

```text
  Claude · Codex · OpenCode · Cline · Pi · Grok
                     │  stdio (MCP)
                     ▼
             litepsm bridge shim
                     │  local authenticated IPC
                     ▼
              litepsm daemon  ──  SQLite (WAL) · policy · secret vault
                     │
                     ▼
        MCP servers · skills · plugins
```

The bridge shim is stateless and never touches your config or database
directly. A single local daemon owns all state, supervises provider processes,
and injects credentials at launch — in memory only.

## Security

- Local IPC restricted to your OS user (Named Pipe DACL / Unix socket `0600`).
- Credentials stored only in the OS vault — never in the database or logs.
- Archive extraction is bounded and rejects traversal, absolute paths,
  symlinks, device files, and case-fold collisions.
- Capability approvals bind to `(capability, schema fingerprint, content
  digest)`; upstream changes force re-approval.

## Requirements

- Node.js ≥ 18 (for the npm launcher), or a supported native binary.
- Windows 10+, macOS 12+, or a modern Linux distribution.

## Links

- **Repository:** https://github.com/sarv-projects/LitePSM
- **Issues:** https://github.com/sarv-projects/LitePSM/issues
- **Catalog API:** https://litepsm.dev/v1/current.json

## License

[Apache-2.0](https://github.com/sarv-projects/LitePSM/blob/main/LICENSE)
