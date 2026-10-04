package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// agents.go — skill install targets for agent hosts.
//
// Directory data is taken from the reference `skills` installer
// (vercel-labs/skills, `src/agents.ts`, MIT), which is the ecosystem's
// de-facto map of where each agent reads `SKILL.md` directories from. We
// deliberately do NOT copy their install strategy (canonical `.agents/skills`
// plus symlinks); we copy straight into each agent's own directory, which is
// simpler to inspect and remove.
//
// Honesty note: this file carries directory paths only. It contains no
// popularity, usage, or trust data, and nothing here should be presented as a
// ranking. Agents are surfaced detected-first, then alphabetically.

// AgentBase selects the root directory a global skills path is resolved against.
type AgentBase int

const (
	// BaseHome resolves against the user's home directory ($HOME).
	BaseHome AgentBase = iota
	// BaseXDG resolves against $XDG_CONFIG_HOME, falling back to ~/.config.
	BaseXDG
	// BaseEnv resolves against the agent's own home environment variable,
	// falling back to DefaultBase under the user's home directory.
	BaseEnv
)

// AgentTarget describes where one agent reads skills from.
type AgentTarget struct {
	ID          string
	DisplayName string
	// ProjectDir is relative to the repository root (project scope).
	ProjectDir string
	// GlobalDir is relative to the resolved base (global scope). Empty means
	// this agent has no documented global skills location.
	GlobalDir string
	// Base chooses how GlobalDir is resolved.
	Base AgentBase
	// EnvVar names the agent home variable for BaseEnv (e.g. CODEX_HOME).
	EnvVar string
	// DefaultBase is the home-relative base used when EnvVar is unset.
	DefaultBase string
	// DetectPaths are home-relative paths whose presence indicates the agent
	// is installed (first match wins).
	DetectPaths []string
	// DetectCwdPaths are project-relative paths that indicate a per-project
	// install of the agent.
	DetectCwdPaths []string
	// Universal is true when ProjectDir is the shared `.agents/skills` tree.
	Universal bool
}

// agentTargets is the verified agent -> skills directory table.
//
// Source: vercel-labs/skills `src/agents.ts` (MIT), read 2026-10-01. Every
// entry here was transcribed from that file. Do not add an entry from memory:
// add it only with a verified source.
var agentTargets = []AgentTarget{
	{ID: "adal", DisplayName: "AdaL", ProjectDir: ".adal/skills", GlobalDir: ".adal/skills", DetectPaths: []string{".adal"}},
	{ID: "aider-desk", DisplayName: "AiderDesk", ProjectDir: ".aider-desk/skills", GlobalDir: ".aider-desk/skills", DetectPaths: []string{".aider-desk"}},
	{ID: "amp", DisplayName: "Amp", ProjectDir: ".agents/skills", GlobalDir: "agents/skills", Base: BaseXDG, DetectPaths: []string{".config/amp"}, Universal: true},
	{ID: "antigravity", DisplayName: "Antigravity", ProjectDir: ".agents/skills", GlobalDir: ".gemini/antigravity/skills", DetectPaths: []string{".gemini/antigravity"}, Universal: true},
	{ID: "antigravity-cli", DisplayName: "Antigravity CLI", ProjectDir: ".agents/skills", GlobalDir: ".gemini/antigravity-cli/skills", DetectPaths: []string{".gemini/antigravity-cli"}, Universal: true},
	{ID: "astrbot", DisplayName: "AstrBot", ProjectDir: "data/skills", GlobalDir: ".astrbot/data/skills", DetectPaths: []string{".astrbot"}},
	{ID: "augment", DisplayName: "Augment", ProjectDir: ".augment/skills", GlobalDir: ".augment/skills", DetectPaths: []string{".augment"}},
	{ID: "autohand-code", DisplayName: "Autohand Code CLI", ProjectDir: ".autohand/skills", GlobalDir: "skills", Base: BaseEnv, EnvVar: "AUTOHAND_HOME", DefaultBase: ".autohand", DetectPaths: []string{".autohand"}},
	{ID: "bob", DisplayName: "IBM Bob", ProjectDir: ".bob/skills", GlobalDir: ".bob/skills", DetectPaths: []string{".bob"}},
	{ID: "claude-code", DisplayName: "Claude Code", ProjectDir: ".claude/skills", GlobalDir: "skills", Base: BaseEnv, EnvVar: "CLAUDE_CONFIG_DIR", DefaultBase: ".claude", DetectPaths: []string{".claude", ".claude.json"}},
	{ID: "cline", DisplayName: "Cline", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPaths: []string{".cline"}, Universal: true},
	{ID: "codearts-agent", DisplayName: "CodeArts Agent", ProjectDir: ".codeartsdoer/skills", GlobalDir: ".codeartsdoer/skills", DetectPaths: []string{".codeartsdoer"}},
	{ID: "codebuddy", DisplayName: "CodeBuddy", ProjectDir: ".codebuddy/skills", GlobalDir: ".codebuddy/skills", DetectPaths: []string{".codebuddy"}},
	{ID: "codemaker", DisplayName: "Codemaker", ProjectDir: ".codemaker/skills", GlobalDir: ".codemaker/skills", DetectPaths: []string{".codemaker"}},
	{ID: "codestudio", DisplayName: "Code Studio", ProjectDir: ".codestudio/skills", GlobalDir: ".codestudio/skills", DetectPaths: []string{".codestudio"}},
	{ID: "codex", DisplayName: "Codex", ProjectDir: ".agents/skills", GlobalDir: "skills", Base: BaseEnv, EnvVar: "CODEX_HOME", DefaultBase: ".codex", DetectPaths: []string{".codex"}, Universal: true},
	{ID: "command-code", DisplayName: "Command Code", ProjectDir: ".commandcode/skills", GlobalDir: ".commandcode/skills", DetectPaths: []string{".commandcode"}},
	{ID: "continue", DisplayName: "Continue", ProjectDir: ".continue/skills", GlobalDir: ".continue/skills", DetectPaths: []string{".continue"}},
	{ID: "cortex", DisplayName: "Cortex Code", ProjectDir: ".cortex/skills", GlobalDir: ".snowflake/cortex/skills", DetectPaths: []string{".snowflake/cortex"}},
	{ID: "crush", DisplayName: "Crush", ProjectDir: ".crush/skills", GlobalDir: "crush/skills", Base: BaseXDG, DetectPaths: []string{".config/crush"}},
	{ID: "cursor", DisplayName: "Cursor", ProjectDir: ".agents/skills", GlobalDir: ".cursor/skills", DetectPaths: []string{".cursor"}, Universal: true},
	{ID: "deepagents", DisplayName: "Deep Agents", ProjectDir: ".agents/skills", GlobalDir: ".deepagents/agent/skills", DetectPaths: []string{".deepagents"}, Universal: true},
	{ID: "devin", DisplayName: "Devin for Terminal", ProjectDir: ".devin/skills", GlobalDir: "devin/skills", Base: BaseXDG, DetectPaths: []string{".config/devin"}},
	{ID: "dexto", DisplayName: "Dexto", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPaths: []string{".dexto"}, Universal: true},
	{ID: "droid", DisplayName: "Droid", ProjectDir: ".agents/skills", GlobalDir: ".factory/skills", DetectPaths: []string{".factory"}, Universal: true},
	{ID: "eve", DisplayName: "Eve", ProjectDir: "agent/skills", GlobalDir: "", DetectCwdPaths: []string{"agent"}},
	{ID: "firebender", DisplayName: "Firebender", ProjectDir: ".agents/skills", GlobalDir: ".firebender/skills", DetectPaths: []string{".firebender"}, Universal: true},
	{ID: "forgecode", DisplayName: "ForgeCode", ProjectDir: ".forge/skills", GlobalDir: ".forge/skills", DetectPaths: []string{".forge"}},
	{ID: "fx", DisplayName: "fx", ProjectDir: ".fx/skills", GlobalDir: ".fx/skills", DetectPaths: []string{".fx"}},
	{ID: "gemini-cli", DisplayName: "Gemini CLI", ProjectDir: ".agents/skills", GlobalDir: ".gemini/skills", DetectPaths: []string{".gemini"}, Universal: true},
	{ID: "github-copilot", DisplayName: "GitHub Copilot", ProjectDir: ".agents/skills", GlobalDir: ".copilot/skills", DetectPaths: []string{".copilot"}, Universal: true},
	{ID: "goose", DisplayName: "Goose", ProjectDir: ".goose/skills", GlobalDir: "goose/skills", Base: BaseXDG, DetectPaths: []string{".config/goose"}},
	{ID: "grok-build", DisplayName: "Grok Build", ProjectDir: ".grok/skills", GlobalDir: "skills", Base: BaseEnv, EnvVar: "GROK_HOME", DefaultBase: ".grok", DetectPaths: []string{".grok"}},
	{ID: "hermes-agent", DisplayName: "Hermes Agent", ProjectDir: ".hermes/skills", GlobalDir: "skills", Base: BaseEnv, EnvVar: "HERMES_HOME", DefaultBase: ".hermes", DetectPaths: []string{".hermes"}},
	{ID: "iflow-cli", DisplayName: "iFlow CLI", ProjectDir: ".iflow/skills", GlobalDir: ".iflow/skills", DetectPaths: []string{".iflow"}},
	{ID: "inference-sh", DisplayName: "inference.sh", ProjectDir: ".inferencesh/skills", GlobalDir: ".inferencesh/skills", DetectPaths: []string{".inferencesh"}},
	{ID: "jazz", DisplayName: "Jazz", ProjectDir: ".jazz/skills", GlobalDir: ".jazz/skills", DetectPaths: []string{".jazz"}},
	{ID: "junie", DisplayName: "Junie", ProjectDir: ".junie/skills", GlobalDir: ".junie/skills", DetectPaths: []string{".junie"}},
	{ID: "kimchi", DisplayName: "Kimchi", ProjectDir: ".kimchi/skills", GlobalDir: ".config/kimchi/harness/skills", DetectPaths: []string{".config/kimchi"}},
	{ID: "kilo", DisplayName: "Kilo Code", ProjectDir: ".agents/skills", GlobalDir: ".kilo/skills", DetectPaths: []string{".kilo", ".kilocode"}, Universal: true},
	{ID: "kimi-code-cli", DisplayName: "Kimi Code CLI", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPaths: []string{".kimi-code", ".kimi"}, Universal: true},
	{ID: "kiro-cli", DisplayName: "Kiro CLI", ProjectDir: ".kiro/skills", GlobalDir: ".kiro/skills", DetectPaths: []string{".kiro"}},
	{ID: "kode", DisplayName: "Kode", ProjectDir: ".kode/skills", GlobalDir: ".kode/skills", DetectPaths: []string{".kode"}},
	{ID: "lingma", DisplayName: "Lingma", ProjectDir: ".lingma/skills", GlobalDir: ".lingma/skills", DetectPaths: []string{".lingma"}},
	{ID: "loaf", DisplayName: "Loaf", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPaths: []string{".loaf"}, Universal: true},
	{ID: "mcpjam", DisplayName: "MCPJam", ProjectDir: ".mcpjam/skills", GlobalDir: ".mcpjam/skills", DetectPaths: []string{".mcpjam"}},
	{ID: "minimax-code", DisplayName: "minimax Code", ProjectDir: ".minimax/skills", GlobalDir: ".minimax/skills", DetectPaths: []string{".minimax"}},
	{ID: "mistral-vibe", DisplayName: "Mistral Vibe", ProjectDir: ".vibe/skills", GlobalDir: "skills", Base: BaseEnv, EnvVar: "VIBE_HOME", DefaultBase: ".vibe", DetectPaths: []string{".vibe"}},
	{ID: "moxby", DisplayName: "Moxby", ProjectDir: ".moxby/skills", GlobalDir: ".moxby/skills", DetectPaths: []string{".moxby"}},
	{ID: "mux", DisplayName: "Mux", ProjectDir: ".mux/skills", GlobalDir: ".mux/skills", DetectPaths: []string{".mux"}},
	{ID: "neovate", DisplayName: "Neovate", ProjectDir: ".neovate/skills", GlobalDir: ".neovate/skills", DetectPaths: []string{".neovate"}},
	{ID: "ona", DisplayName: "Ona", ProjectDir: ".ona/skills", GlobalDir: ".ona/skills", DetectPaths: []string{".ona"}},
	{ID: "opencode", DisplayName: "OpenCode", ProjectDir: ".agents/skills", GlobalDir: "opencode/skills", Base: BaseXDG, DetectPaths: []string{".config/opencode"}, Universal: true},
	{ID: "openhands", DisplayName: "OpenHands", ProjectDir: ".openhands/skills", GlobalDir: ".openhands/skills", DetectPaths: []string{".openhands"}},
	{ID: "pi-agent", DisplayName: "Pi Agent", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPaths: []string{".pi"}, Universal: true},
	{ID: "pochi", DisplayName: "Pochi", ProjectDir: ".pochi/skills", GlobalDir: ".pochi/skills", DetectPaths: []string{".pochi"}},
	{ID: "posit-assistant", DisplayName: "Posit Assistant", ProjectDir: ".posit/assistant/skills", GlobalDir: ".posit/assistant/skills", DetectPaths: []string{".posit/assistant", ".positai"}},
	{ID: "promptscript", DisplayName: "PromptScript", ProjectDir: ".agents/skills", GlobalDir: "", DetectCwdPaths: []string{".promptscript", "promptscript.yaml"}, Universal: true},
	{ID: "qoder", DisplayName: "Qoder", ProjectDir: ".qoder/skills", GlobalDir: ".qoder/skills", DetectPaths: []string{".qoder"}},
	{ID: "qoder-cn", DisplayName: "Qoder CN", ProjectDir: ".qoder/skills", GlobalDir: ".qoder-cn/skills", DetectPaths: []string{".qoder-cn"}},
	{ID: "qwen-code", DisplayName: "Qwen Code", ProjectDir: ".qwen/skills", GlobalDir: ".qwen/skills", DetectPaths: []string{".qwen"}},
	{ID: "reasonix", DisplayName: "Reasonix", ProjectDir: ".reasonix/skills", GlobalDir: ".reasonix/skills", DetectPaths: []string{".reasonix"}},
	{ID: "replit", DisplayName: "Replit", ProjectDir: ".agents/skills", GlobalDir: "agents/skills", Base: BaseXDG, DetectPaths: []string{".replit"}, DetectCwdPaths: []string{".replit"}, Universal: true},
	{ID: "roo", DisplayName: "Roo Code", ProjectDir: ".roo/skills", GlobalDir: ".roo/skills", DetectPaths: []string{".roo"}},
	{ID: "rovodev", DisplayName: "Rovo Dev", ProjectDir: ".rovodev/skills", GlobalDir: ".rovodev/skills", DetectPaths: []string{".rovodev"}},
	{ID: "sarvam-code", DisplayName: "Sarvam Code", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", Base: BaseEnv, EnvVar: "SARVAM_HOME", DefaultBase: ".sarvam", DetectPaths: []string{".sarvam"}, Universal: true},
	{ID: "tabnine-cli", DisplayName: "Tabnine CLI", ProjectDir: ".tabnine/agent/skills", GlobalDir: ".tabnine/agent/skills", DetectPaths: []string{".tabnine"}},
	{ID: "terramind", DisplayName: "Terramind", ProjectDir: ".terramind/skills", GlobalDir: ".terramind/skills", DetectPaths: []string{".terramind"}},
	{ID: "tinycloud", DisplayName: "Tinycloud", ProjectDir: ".tinycloud/skills", GlobalDir: ".tinycloud/skills", DetectPaths: []string{".tinycloud"}},
	{ID: "trae", DisplayName: "Trae", ProjectDir: ".trae/skills", GlobalDir: ".trae/skills", DetectPaths: []string{".trae"}},
	{ID: "trae-cn", DisplayName: "Trae CN", ProjectDir: ".trae/skills", GlobalDir: ".trae-cn/skills", DetectPaths: []string{".trae-cn"}},
	{ID: "windsurf", DisplayName: "Windsurf", ProjectDir: ".windsurf/skills", GlobalDir: ".codeium/windsurf/skills", DetectPaths: []string{".codeium/windsurf"}},
	{ID: "zcode", DisplayName: "ZCode", ProjectDir: ".zcode/skills", GlobalDir: ".zcode/skills", DetectPaths: []string{".zcode"}},
	{ID: "zed", DisplayName: "Zed", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPaths: []string{".config/zed", "AppData/Roaming/Zed"}, Universal: true},
	{ID: "zencoder", DisplayName: "Zencoder", ProjectDir: ".zencoder/skills", GlobalDir: ".zencoder/skills", DetectPaths: []string{".zencoder"}},
	{ID: "zenflow", DisplayName: "Zenflow", ProjectDir: ".zencoder/skills", GlobalDir: ".zencoder/skills", DetectPaths: []string{".zencoder"}},
}

// openclawTarget resolves at runtime: the agent's home dir was renamed twice,
// so the global path depends on which one exists.
var openclawTarget = AgentTarget{
	ID:          "openclaw",
	DisplayName: "OpenClaw",
	ProjectDir:  "skills",
	GlobalDir:   ".openclaw/skills",
	DetectPaths: []string{".openclaw", ".clawdbot", ".moltbot"},
}

// AgentTargets returns every known agent skill target, alphabetically by
// display name. This is a directory map, not a ranking.
func AgentTargets() []AgentTarget {
	out := make([]AgentTarget, 0, len(agentTargets)+1)
	out = append(out, agentTargets...)
	out = append(out, openclawTarget)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && lower(out[j].DisplayName) < lower(out[j-1].DisplayName); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// LookupAgent returns the agent target for an id.
func LookupAgent(id string) (AgentTarget, bool) {
	for _, a := range AgentTargets() {
		if a.ID == id {
			return a, true
		}
	}
	return AgentTarget{}, false
}

// AgentIDs returns all known agent ids, sorted.
func AgentIDs() []string {
	targets := AgentTargets()
	out := make([]string, 0, len(targets))
	for _, a := range targets {
		out = append(out, a.ID)
	}
	return out
}

// openClawGlobalDir picks the renamed OpenClaw home that actually exists.
func openClawGlobalDir(home string) string {
	for _, cand := range []string{".openclaw", ".clawdbot", ".moltbot"} {
		if pathExists(filepath.Join(home, cand)) {
			return filepath.Join(home, cand, "skills")
		}
	}
	return filepath.Join(home, ".openclaw", "skills")
}

// AgentSkillDir returns the skill directory for an agent at the given scope.
// Unknown agents return ok=false; agents without a documented global location
// return ok=false for scope="global".
//
// PRECEDENCE, because it surprises callers: documented per-agent home overrides
// (CODEX_HOME, CLAUDE_CONFIG_DIR, XDG_CONFIG_HOME, ...) WIN over the `home`
// argument. That is intentional for the CLI -- those variables are a real user
// escape hatch and match the convention each agent already follows -- but it
// means this function is NOT a pure function of its arguments. Any test must
// neutralise the ambient environment (see useTempHome), and any caller that
// needs a guaranteed home must set the override explicitly rather than assume
// the argument is honoured.
func AgentSkillDir(agentID, scope, projectRoot, home string) (string, bool) {
	a, ok := LookupAgent(agentID)
	if !ok {
		return "", false
	}
	if scope != "global" {
		if a.ProjectDir == "" {
			return "", false
		}
		return filepath.Join(projectRoot, filepath.FromSlash(a.ProjectDir)), true
	}
	if a.GlobalDir == "" {
		return "", false
	}
	if a.ID == "openclaw" {
		return openClawGlobalDir(home), true
	}
	var base string
	switch a.Base {
	case BaseXDG:
		base = xdgConfigHome(home)
	case BaseEnv:
		base = home
		if a.EnvVar != "" {
			if v := strings.TrimSpace(os.Getenv(a.EnvVar)); v != "" {
				base = v
			} else if a.DefaultBase != "" {
				base = filepath.Join(home, filepath.FromSlash(a.DefaultBase))
			}
		}
	default:
		base = home
	}
	dir := filepath.Join(base, filepath.FromSlash(a.GlobalDir))
	if strings.HasPrefix(a.GlobalDir, ".") && !strings.HasPrefix(dir, base+string(filepath.Separator)) {
		// GlobalDir like ".gemini/skills" is already home-relative.
		dir = filepath.Join(home, filepath.FromSlash(a.GlobalDir))
	}
	return dir, true
}

// xdgConfigHome returns $XDG_CONFIG_HOME or ~/.config.
func xdgConfigHome(home string) string {
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return v
	}
	return filepath.Join(home, ".config")
}

// AgentInstalled reports whether an agent appears installed, checking home
// paths first and then project-relative paths.
func AgentInstalled(a AgentTarget, projectRoot, home string) bool {
	for _, p := range a.DetectPaths {
		if pathExists(filepath.Join(home, filepath.FromSlash(p))) {
			return true
		}
	}
	for _, p := range a.DetectCwdPaths {
		if projectRoot != "" && pathExists(filepath.Join(projectRoot, filepath.FromSlash(p))) {
			return true
		}
	}
	return false
}

func lower(s string) string { return strings.ToLower(s) }
