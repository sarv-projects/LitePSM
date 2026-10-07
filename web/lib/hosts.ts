// Canonical agent-host integration model for the Market UI.
//
// The host list is GENERATED from the compiled-in Go registry, not hand-written.
// Regenerate with:
//
//     go run scripts/gen_hosts_ts.go
//
// It used to be a hand-maintained array of six hosts while the registry had
// grown to fifty, so the public site understated its own capability by 8x. A
// generated file cannot drift, and `npm run type-check` plus the catalog build
// both read the same source.
//
// LiteSPM registers ONE `litespm` bridge entry per host. Individual capabilities
// are resolved by the daemon at runtime, so per-listing snippets are identical
// regardless of the capability being viewed.

import generated from "../data/hosts.json";
import generatedSkillTargets from "../data/skill-targets.json";

export type PlatformOS = "win" | "mac" | "linux";
export type HostFormat = "json" | "toml";
export type HostShape = "object" | "local-array" | "stdio-typed" | "command-string" | "";

export interface HostAdapter {
  /** Registry id passed to `litespm bridge stdio --host <id>`. */
  id: string;
  name: string;
  kind: HostFormat;
  /** Dotted key path that holds MCP server definitions. */
  keyPath: string;
  /** How one server entry is written for this host. */
  shape: HostShape;
  /**
   * Unix-resolved user-scope config path, with the home directory shown as "~".
   * Windows paths are intentionally absent: the registry refuses to guess them
   * (see ARCH/30), so the UI does not either.
   */
  userPath: string;
  /** True when the host only documents repo-local configuration. */
  projectOnly?: boolean;
  /** The upstream source this adapter was verified against. */
  docsUrl?: string;
  /** True when the adapter is data-driven rather than hand-written. */
  generic: boolean;
  /** True when the MCP key is nested below another object. */
  nested: boolean;
}

/** Shape of one row in the generated web/data/hosts.json. */
interface GeneratedHost {
  id: string;
  name: string;
  format: string;
  keyPath?: string;
  shape?: string;
  userPath?: string;
  projectOnly?: boolean;
  docsUrl?: string;
  generic: boolean;
}

/**
 * Curated per-OS path overrides. Only hosts whose Windows and macOS locations
 * were read from vendor documentation appear here. Everything else falls back
 * to `userPath` (the Unix form), and the UI labels it as such rather than
 * inventing a Windows location.
 */
const PATH_OVERRIDES: Record<string, Partial<Record<PlatformOS, string>>> = {
  "claude-code": { win: "%USERPROFILE%\\.claude.json" },
  codex: { win: "%USERPROFILE%\\.codex\\config.toml" },
  opencode: { win: "%USERPROFILE%\\.config\\opencode\\opencode.json" },
  // Cline moved the file out of VS Code's globalStorage into the shared
  // ~/.cline/data/settings path every current client reads (see
  // internal/host/cline.go); the previous win/mac overrides pointed at a
  // location current Cline only reads once, as a legacy migration.
  cline: { win: "%USERPROFILE%\\.cline\\data\\settings\\cline_mcp_settings.json" },
  "pi-agent": { win: "%USERPROFILE%\\.pi\\agent\\mcp.json" },
  "grok-build": { win: "%USERPROFILE%\\.grok\\config.toml" },
};

export const HOSTS: HostAdapter[] = (generated as GeneratedHost[]).map((h) => {
  // keyPath and shape are emitted by scripts/gen_hosts_ts.go for every
  // adapter — the six hand-written ones included — from the same registry the
  // binary writes with, so this file no longer restates them. The fallbacks
  // below only cover a host the registry declares no layout for.
  const fallback = h.format === "toml" ? "mcp_servers" : "mcpServers";
  const keyPath = h.keyPath || fallback;
  const shape: HostShape = (h.shape as HostShape) || "object";
  return {
    id: h.id,
    name: h.name,
    kind: h.format === "toml" ? "toml" : "json",
    keyPath,
    shape,
    userPath: h.userPath || "",
    projectOnly: h.projectOnly,
    docsUrl: h.docsUrl,
    generic: h.generic,
    nested: keyPath.includes("."),
  };
});

/** Resolve the documented config path for one host on one platform. */
export function hostPath(host: HostAdapter, os: PlatformOS): string {
  const override = PATH_OVERRIDES[host.id]?.[os];
  if (override) return override;
  return host.userPath;
}

/**
 * Skill install targets, generated from internal/skills/agents.go. A different
 * registry from bridge adapters: these are hosts with a skills directory we can
 * write a SKILL.md into, which is a smaller and differently-shaped set than
 * "hosts whose MCP config we can edit".
 */
export const SKILL_TARGETS: Array<{ id: string; displayName: string; universal?: boolean }> =
  generatedSkillTargets as Array<{ id: string; displayName: string; universal?: boolean }>;

/**
 * Which agent hosts a capability can be installed into.
 *
 * This is a property of the KIND, not of the individual row:
 *
 *   mcp     every bridge adapter — the bridge is one stdio entry, so any host
 *           whose config we can edit can run any MCP server.
 *   skill   every skill target — only hosts with a documented skills directory.
 *   plugin  publisher-declared, because a bundle's reach genuinely varies.
 *
 * The builder previously stamped the same seven host names onto all 4,079 MCP
 * servers, which made the public site report nine hosts while the binary shipped
 * fifty. Deriving it here means the claim cannot drift from the registries.
 */
export function hostsFor(item: { kind: string; compatibleHosts?: string[] }): string[] {
  if (item.kind === "mcp") return HOSTS.map((h) => h.name);
  if (item.kind === "skill") return SKILL_TARGETS.map((t) => t.displayName);
  return item.compatibleHosts || [];
}

/** Every distinct host name this catalog can install into. */
export function allHostNames(): string[] {
  const set = new Set<string>();
  for (const h of HOSTS) set.add(h.name);
  for (const t of SKILL_TARGETS) set.add(t.displayName);
  return Array.from(set).sort((a, b) => a.localeCompare(b));
}

/** True when the path shown for `os` came from vendor docs rather than expansion. */
export function hasDocumentedPath(host: HostAdapter, os: PlatformOS): boolean {
  return Boolean(PATH_OVERRIDES[host.id]?.[os]);
}

export function getHost(id: string): HostAdapter {
  return HOSTS.find((h) => h.id === id) ?? HOSTS[0];
}

/**
 * The catalog names hosts as its publishers wrote them ("Codex"), while the
 * adapter table uses display names ("OpenAI Codex"). Aliases are what let a
 * "managed" badge be attached to the right row instead of being silently
 * dropped for want of an exact string match. Built from the generated table so
 * every registered host is resolvable without a hand-kept list.
 */
const HOST_ALIASES: Record<string, string> = (() => {
  const map: Record<string, string> = {};
  for (const h of HOSTS) {
    const add = (key: string) => {
      const k = key.trim().toLowerCase();
      if (k && !map[k]) map[k] = h.id;
    };
    add(h.id);
    add(h.id.replace(/-/g, " "));
    add(h.name);
    add(h.name.replace(/\s+/g, ""));
  }
  // Publisher spellings that differ from both the id and the display name.
  Object.assign(map, {
    "openai codex": "codex",
    claudecode: "claude-code",
    "claude code": "claude-code",
    piagent: "pi-agent",
    "pi agent": "pi-agent",
    grokbuild: "grok-build",
    "grok build": "grok-build",
    "kilo code": "kilo",
    "roo code": "roo",
    "github copilot": "github-copilot",
    "gemini cli": "gemini-cli",
  });
  return map;
})();

export function resolveHost(name: string): HostAdapter | undefined {
  const id = HOST_ALIASES[name.trim().toLowerCase()];
  if (!id) return undefined;
  return HOSTS.find((h) => h.id === id);
}

/**
 * Nest `leaf` under a dotted key path, e.g. ("amp.mcpServers", v) →
 * `{ amp: { mcpServers: v } }`. Hosts genuinely differ here (Amp nests under
 * `amp`, Crush reads `mcp` at the root), so the path comes from the registry
 * rather than being assumed.
 */
function nest(keyPath: string, leaf: unknown): Record<string, unknown> {
  const parts = keyPath.split(".").filter(Boolean);
  if (parts.length === 0) return leaf as Record<string, unknown>;
  let acc: unknown = leaf;
  for (let i = parts.length - 1; i >= 0; i -= 1) {
    acc = { [parts[i]]: acc };
  }
  return acc as Record<string, unknown>;
}

/** The single `litespm` server entry, in this host's own shape. */
export function bridgeEntry(host: HostAdapter, binary = "litespm"): unknown {
  const args = ["bridge", "stdio", "--host", host.id];
  switch (host.shape) {
    case "local-array":
      return { type: "local", command: [binary, ...args] };
    case "stdio-typed":
      return { type: "stdio", command: binary, args };
    case "command-string":
      return `${binary} ${args.join(" ")}`;
    default:
      return { command: binary, args };
  }
}

/** Correct, host-accurate configuration for the single litespm bridge entry. */
export function bridgeSnippet(host: HostAdapter, binary = "litespm", os: PlatformOS = "linux"): string {
  const path = hostPath(host, os);
  const keyPath = host.keyPath || "mcpServers";

  if (host.kind === "toml") {
    const table = keyPath === "mcp_servers" ? "mcp_servers.litespm" : `${keyPath}.litespm`;
    return `# ${path}\n[${table}]\ncommand = "${binary}"\nargs = ["bridge", "stdio", "--host", "${host.id}"]\n`;
  }

  const body = nest(keyPath, { litespm: bridgeEntry(host, binary) });
  return `# ${path}\n${JSON.stringify(body, null, 2)}`;
}
