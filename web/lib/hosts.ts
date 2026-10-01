// Canonical agent-host integration model for the Market UI.
// This MUST stay in sync with the compiled-in Go adapters in internal/host/*.go:
// cline, pi-agent, grok-build, claude-code, codex, opencode.
//
// LitePSM registers ONE `litepsm` bridge entry per host. Individual
// capabilities are resolved by the daemon at runtime, so per-listing snippets
// are intentionally identical regardless of the capability being viewed.

import { jsonKey, tomlKey } from "./format";

export type PlatformOS = "win" | "mac" | "linux";

export interface HostAdapter {
  /** Registry id passed to `litepsm bridge stdio --host <id>`. */
  id: string;
  name: string;
  kind: "json" | "toml";
  /** Config file path per platform. */
  paths: Record<PlatformOS, string>;
  /** v2 nested layout (opencode only). */
  nested?: boolean;
}

export const HOSTS: HostAdapter[] = [
  {
    id: "claude-code",
    name: "Claude Code",
    kind: "json",
    paths: {
      win: "%USERPROFILE%\\.claude.json",
      mac: "~/.claude.json",
      linux: "~/.claude.json",
    },
  },
  {
    id: "codex",
    name: "OpenAI Codex",
    kind: "toml",
    paths: {
      win: "%USERPROFILE%\\.codex\\config.toml",
      mac: "~/.codex/config.toml",
      linux: "~/.codex/config.toml",
    },
  },
  {
    id: "opencode",
    name: "OpenCode",
    kind: "json",
    paths: {
      win: "%USERPROFILE%\\.config\\opencode\\opencode.json",
      mac: "~/.config/opencode/opencode.json",
      linux: "~/.config/opencode/opencode.json",
    },
    nested: true,
  },
  {
    id: "cline",
    name: "Cline",
    kind: "json",
    paths: {
      win: "%APPDATA%\\Code\\User\\globalStorage\\saoudrizwan.claude-dev\\settings\\cline_mcp_settings.json",
      mac: "~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json",
      linux: "~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json",
    },
  },
  {
    id: "pi-agent",
    name: "Pi Agent",
    kind: "json",
    paths: {
      win: "%USERPROFILE%\\.pi\\agent\\mcp.json",
      mac: "~/.pi/agent/mcp.json",
      linux: "~/.pi/agent/mcp.json",
    },
  },
  {
    id: "grok-build",
    name: "Grok Build",
    kind: "toml",
    paths: {
      win: "%USERPROFILE%\\.grok\\config.toml",
      mac: "~/.grok/config.toml",
      linux: "~/.grok/config.toml",
    },
  },
];

export function getHost(id: string): HostAdapter {
  return HOSTS.find((h) => h.id === id) ?? HOSTS[0];
}

/** Correct, host-accurate configuration for the single litepsm bridge entry. */
export function bridgeSnippet(host: HostAdapter, binary = "litepsm"): string {
  const args = ["bridge", "stdio", "--host", host.id];

  if (host.kind === "toml") {
    return `# ${host.paths.linux}\n[mcp_servers.litepsm]\ncommand = "${binary}"\nargs = [${args
      .map((a) => `"${a}"`)
      .join(", ")}]\n`;
  }

  if (host.id === "opencode") {
    // OpenCode requires a single command array plus an explicit transport type,
    // nested under mcp.servers in the v2 layout.
    return JSON.stringify(
      { mcp: { servers: { litepsm: { type: "local", command: [binary, ...args] } } } },
      null,
      2
    );
  }

  return JSON.stringify({ mcpServers: { litepsm: { command: binary, args } } }, null, 2);
}

/** Native (unmanaged) snippet, shown only when the user opts into raw config. */
export function nativeSnippet(
  host: HostAdapter,
  slug: string,
  command: string | undefined,
  args: string[] | undefined
): string {
  const cmd = command || "npx";
  const argv = args && args.length ? args : [cmd === "npx" ? "-y" : "", slug].filter(Boolean);

  if (host.kind === "toml") {
    const key = tomlKey(slug);
    return `# ${host.paths.linux}\n[mcp_servers.${key}]\ncommand = "${cmd}"\nargs = [${argv
      .map((a) => `"${a}"`)
      .join(", ")}]\n`;
  }

  if (host.id === "opencode") {
    return JSON.stringify(
      { mcp: { servers: { [jsonKey(slug)]: { type: "local", command: [cmd, ...argv] } } } },
      null,
      2
    );
  }

  return JSON.stringify({ mcpServers: { [jsonKey(slug)]: { command: cmd, args: argv } } }, null, 2);
}
