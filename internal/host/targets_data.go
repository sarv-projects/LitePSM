package host

// targets_data.go — agent bridge targets, each traced to a primary source.
//
// Every row here is claimed to be checked against that agent's own
// documentation or repository; DocsURL records the source. A target that could
// not be verified is deliberately ABSENT rather than filled with a guess: an
// unverified config path produces an installer that silently writes to the
// wrong place, which is worse than reporting the agent as unsupported.
//
// That claim was AUDITED on 2026-10-06, host by host, against live vendor
// documentation (see ARCH/30 §8.1). The first pass had not been done against
// primary sources for every row, and the audit found real defects on 14 rows
// plus three in the shared writer — including an invented OpenCode layout and a
// flat-key container that was being written as a nested object. The rule this
// file now lives by: a row is only correct if a maintainer can point at the
// vendor sentence that says so. Where a vendor contradicts itself (Cursor and
// Firebender publish a field table marking `type` required while every example
// omits it), the row records the contradiction instead of guessing; those are
// listed in ARCH/30 §8.1.
//
// Deliberately excluded, with the reason
//	recorded so nobody re-adds them blindly:
//
//	autohand-code   mcp.servers is an array of objects and the config is
//	                multi-format (toml/yaml/json); needs its own reader.
//	continue        YAML config.yaml where mcpServers is a LIST, plus separate
//	                per-block workspace files.
//	dexto           MCP lives inside the agent's own YAML, no user-scope file.
//	eve             MCP is code-defined (agent/connections/*.ts), no config file.
//	goose           YAML, key "extensions", uses "cmd" not "command", no SSE.
//	hermes-agent    YAML config.
//	lingma          the on-disk path of lingma_mcp.json is never published.
//	loaf            no vendor site or repository could be reached.
//	mcpjam          manages and tests MCP servers; it is not an MCP host.
//	minimax-code    the documented path is hedged ("for example ~/.minimax/mcp.json").
//	mistral-vibe    TOML with [[mcp_servers]] as an array of tables, and a
//	                project file that replaces rather than deep-merges.
//	moxby           browser extension with no MCP client config.
//	promptscript    MCP is an unimplemented roadmap item (v0.5), not shipped.
//	reasonix        three file formats with legacy backfill and [[plugins]] semantics.
//	replit          MCP is configured server-side; there is no client config file.
//	roo (user)      user-scope path lives in VS Code globalStorage and is undocumented.
//	sarvam-code     product unreleased ("coming soon" placeholders).
//	terramind       not an agent: IBM's Earth Observation foundation model.
//	tinycloud       vendor site unreachable; npm packages are infra tooling.
//	trae / trae-cn  user-scope path is not published (project scope is verified
//	                but our setup flow is user-scope); needs follow-up.
//	vtcode          TOML with [[mcp.providers]] array-of-tables, not a map.
//	windsurf        Current docs point at ~/.config/devin/mcp_config.json —
//	                the SAME file the devin row owns. Registering both would
//	                make two hosts claim one config file.
//	adal            MCP works but the on-disk server config is undocumented.

import (
	"os"
	"path/filepath"
	"runtime"
)

// xdgConfigDir returns the XDG config base, honouring XDG_CONFIG_HOME.
func xdgConfigDir(home string) string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	return filepath.Join(home, ".config")
}

// windowsAppData returns %APPDATA% on native Windows and "" elsewhere. It is
// gated on the OS rather than on the variable merely being set, because APPDATA
// is meaningless on Unix: honouring it there would silently redirect a config
// path to the wrong place.
func windowsAppData() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return os.Getenv("APPDATA")
}

// windowsLocalAppData returns %LOCALAPPDATA% on native Windows and "" elsewhere.
// It is gated on the OS for the same reason windowsAppData is: on Unix
// LOCALAPPDATA is meaningless, and honouring it there would silently redirect a
// config path. Crush reads its Windows user-global config from this directory,
// not from %APPDATA%.
func windowsLocalAppData() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return os.Getenv("LOCALAPPDATA")
}

// firstExisting returns the first path that exists, else the first candidate.
func firstExisting(candidates ...string) string {
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// verifiedBridgeTargets is the data-driven target registry.
var verifiedBridgeTargets = []BridgeTarget{
	{
		ID: "aider-desk", Name: "AiderDesk", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			// The vendor documents AIDER_DESK_HOME_DIR as the relocation mechanism.
			if dir := os.Getenv("AIDER_DESK_HOME_DIR"); dir != "" {
				return filepath.Join(dir, "mcp-servers.json")
			}
			return filepath.Join(home, ".aider-desk", "mcp-servers.json")
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".aider-desk", "mcp-servers.json") },
		DetectPaths:    []string{".aider-desk"},
		DetectBinaries: []string{"aider-desk"},
		DocsURL:        "https://aiderdesk.hotovo.com/docs/agent-mode/mcp-servers",
		Note:           "Project scope merges over user scope.",
	},
	{
		ID: "amp", Name: "Amp", Format: FormatJSON,
		UserKey: []string{"amp", "mcpServers"}, ProjectKey: []string{"amp", "mcpServers"},
		Shape: ShapeObject, TolerateComments: true, FlatKey: true,
		UserPath: func(home string) string {
			return firstExisting(filepath.Join(xdgConfigDir(home), "amp", "settings.json"), filepath.Join(xdgConfigDir(home), "amp", "settings.jsonc"))
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".amp", "settings.json") },
		DetectPaths:    []string{".config/amp"},
		DetectBinaries: []string{"amp"},
		DocsURL:        "https://ampcode.com/docs/customize/mcp",
		Note:           "The container is the literal top-level key \"amp.mcpServers\": Amp's published schema sets additionalProperties:false, so a nested \"amp\" object is both ignored and invalid. Remote definitions are server-side and not file-configurable.",
	},
	{
		ID: "antigravity", Name: "Antigravity", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:            ShapeObject,
		UserPath:         func(home string) string { return filepath.Join(home, ".gemini", "config", "mcp_config.json") },
		ProjectPath:      func(root string) string { return filepath.Join(root, ".agents", "mcp_config.json") },
		DetectPaths:      []string{".gemini/antigravity"},
		SharedConfigWith: []string{"antigravity-cli"},
		DocsURL:          "https://antigravity.google/docs/mcp",
		Note:             "Remote servers must use \"serverUrl\"; \"url\"/\"httpUrl\" are not accepted. Windows path is ~ expansion.",
	},
	{
		ID: "antigravity-cli", Name: "Antigravity CLI", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:            ShapeObject,
		UserPath:         func(home string) string { return filepath.Join(home, ".gemini", "config", "mcp_config.json") },
		ProjectPath:      func(root string) string { return filepath.Join(root, ".agents", "mcp_config.json") },
		DetectPaths:      []string{".gemini/antigravity-cli"},
		DetectBinaries:   []string{"agy"},
		SharedConfigWith: []string{"antigravity"},
		DocsURL:          "https://antigravity.google/docs/mcp",
		Note:             "Shares one config file with the Antigravity IDE; see the antigravity row.",
	},
	{
		ID: "astrbot", Name: "AstrBot", Format: FormatJSON,
		UserKey: []string{"mcpServers"},
		Shape:   ShapeObject,
		UserPath: func(home string) string {
			// AstrBot resolves its root as ASTRBOT_ROOT, else the process working
			// directory (else ~/.astrbot in the packaged desktop runtime). Falling
			// back to $HOME, as this row did, targeted ~/data/mcp_server.json,
			// which AstrBot never opens in a source or container deployment.
			root := os.Getenv("ASTRBOT_ROOT")
			if root == "" {
				if cwd, err := os.Getwd(); err == nil {
					root = cwd
				} else {
					root = home
				}
			}
			return filepath.Join(root, "data", "mcp_server.json")
		},
		DetectPaths: []string{".astrbot"},
		DocsURL:     "https://github.com/AstrBotDevs/AstrBot/wiki/en-use-mcp",
		Note:        "A chat-bot framework, not a coding agent. Config lives under ASTRBOT_ROOT, not $HOME.",
	},
	{
		ID: "augment", Name: "Augment", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".augment", "settings.json") },
		DetectPaths:    []string{".augment"},
		DetectBinaries: []string{"auggie"},
		DocsURL:        "https://docs.augmentcode.com/cli/integrations",
		Note:           "Targets the Auggie CLI config; the IDE extension stores MCP separately.",
	},
	{
		ID: "cursor", Name: "Cursor", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".cursor", "mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".cursor", "mcp.json") },
		DetectPaths:    []string{".cursor"},
		DetectBinaries: []string{"agent"},
		DocsURL:        "https://cursor.com/docs/mcp",
		Note:           "Strict JSON (no comments). IDE and the Cursor CLI share this one file.",
	},
	{
		ID: "deepagents", Name: "Deep Agents", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".deepagents", ".mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".mcp.json") },
		DetectPaths:    []string{".deepagents"},
		DetectBinaries: []string{"dcode"},
		DocsURL:        "https://docs.langchain.com/oss/deepagents/code/mcp-tools",
		Note:           "Project-level MCP is default-deny trust-gated by the host.",
	},
	{
		ID: "devin", Name: "Devin", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			if ad := windowsAppData(); ad != "" {
				return filepath.Join(ad, "devin", "mcp_config.json")
			}
			return filepath.Join(xdgConfigDir(home), "devin", "mcp_config.json")
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".devin", "mcp_config.json") },
		DetectPaths:    []string{".config/devin"},
		DetectBinaries: []string{"devin"},
		DocsURL:        "https://docs.devin.ai/cli/extensibility/mcp/configuration",
		Note:           "MCP moved out of config.json into dedicated mcp_config.json files. The legacy Cascade agent also reads windsurf's config, which is this same file.",
	},
	{
		ID: "droid", Name: "Droid", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".factory", "mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".factory", "mcp.json") },
		DetectPaths:    []string{".factory"},
		DetectBinaries: []string{"droid"},
		DocsURL:        "https://docs.factory.ai/harness/mcp",
		Note:           "Three-level precedence (user, folder, project); user wins per server name.",
	},
	{
		ID: "firebender", Name: "Firebender", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:       ShapeObject,
		UserPath:    func(home string) string { return filepath.Join(home, ".firebender", "firebender.json") },
		ProjectPath: func(root string) string { return filepath.Join(root, "firebender.json") },
		DetectPaths: []string{".firebender"},
		DocsURL:     "https://docs.firebender.com/context/mcp/overview",
		Note:        "Vendor docs contradict themselves (mcp.json vs firebender.json); the syntax reference and config-locations section agree on firebender.json.",
	},
	{
		ID: "forgecode", Name: "ForgeCode", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, "forge", ".mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".mcp.json") },
		DetectPaths:    []string{"forge"},
		DetectBinaries: []string{"forge"},
		DocsURL:        "https://github.com/tailcallhq/forgecode",
		Note:           "Other settings live in ~/forge/.forge.toml; MCP is .mcp.json.",
	},
	{
		ID: "fx", Name: "fx", Format: FormatJSON,
		// fx reads `mcp` in the user file and `mcpServers` in a project .mcp.json.
		UserKey: []string{"mcp"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeLocalArray,
		UserPath:       func(home string) string { return filepath.Join(home, ".fx", "mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".mcp.json") },
		DetectPaths:    []string{".fx"},
		DetectBinaries: []string{"fx"},
		DocsURL:        "https://fx.sh/docs/capabilities/mcp",
		Note:           "Key is \"mcp\" (legacy mcpServers still accepted). Entry uses a combined command array.",
	},
	{
		ID: "gemini-cli", Name: "Gemini CLI", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			if v := os.Getenv("GEMINI_CLI_HOME"); v != "" {
				return filepath.Join(v, ".gemini", "settings.json")
			}
			return filepath.Join(home, ".gemini", "settings.json")
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".gemini", "settings.json") },
		DetectPaths:    []string{".gemini"},
		DetectBinaries: []string{"gemini"},
		DocsURL:        "https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md",
		Note:           "GEMINI_CLI_HOME redirects the whole state dir. Also has a global \"mcp\" allow/exclude object we do not touch.",
	},
	{
		ID: "github-copilot", Name: "GitHub Copilot CLI", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			if v := os.Getenv("COPILOT_HOME"); v != "" {
				return filepath.Join(v, "mcp-config.json")
			}
			return filepath.Join(home, ".copilot", "mcp-config.json")
		},
		ProjectPath: func(root string) string {
			return firstExisting(filepath.Join(root, ".mcp.json"), filepath.Join(root, ".github", "mcp.json"))
		},
		DetectPaths:    []string{".copilot"},
		DetectBinaries: []string{"copilot"},
		DocsURL:        "https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers",
		Note:           "Project scope beats user scope; .mcp.json beats .github/mcp.json.",
	},
	{
		ID: "iflow-cli", Name: "iFlow CLI", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".iflow", "settings.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".iflow", "settings.json") },
		DetectPaths:    []string{".iflow"},
		DetectBinaries: []string{"iflow"},
		DocsURL:        "https://github.com/iflow-ai/iflow-cli",
		Note:           "Vendor docs list two conflicting layouts (settings.json vs .iflow/mcp/config.json); settings.json is what we target.",
	},
	{
		ID: "jazz", Name: "Jazz", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".agents", "mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".agents", "mcp.json") },
		DetectPaths:    []string{".jazz"},
		DetectBinaries: []string{"jazz"},
		DocsURL:        "https://github.com/lvndry/jazz",
		Note:           "Uses the universal .agents/mcp.json. Enable/disable state lives in a separate config we do not touch.",
	},
	{
		ID: "junie", Name: "Junie", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".junie", "mcp", "mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".junie", "mcp", "mcp.json") },
		DetectPaths:    []string{".junie"},
		DetectBinaries: []string{"junie"},
		DocsURL:        "https://junie.jetbrains.com/docs/junie-cli-mcp-configuration.html",
		Note:           "Note the nested mcp/ directory. Legacy flat ~/.junie/mcp.json also referenced.",
	},
	{
		ID: "kimchi", Name: "Kimchi", Format: FormatJSON,
		UserKey:        []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".config", "kimchi", "harness", "mcp.json") },
		DetectPaths:    []string{".config/kimchi"},
		DetectBinaries: []string{"kimchi"},
		DocsURL:        "https://docs.kimchi.dev/docs/coding-mcp-servers",
		Note:           "No documented env override for the harness path.",
	},
	{
		// Kilo documents kilo.jsonc as a supported filename; that is the reason to
		// use it, so a commented config must be readable.
		ID: "kilo", Name: "Kilo Code", Format: FormatJSON, TolerateComments: true,
		UserKey: []string{"mcp"}, ProjectKey: []string{"mcp"},
		Shape: ShapeLocalArray,
		UserPath: func(home string) string {
			return firstExisting(filepath.Join(xdgConfigDir(home), "kilo", "kilo.json"), filepath.Join(xdgConfigDir(home), "kilo", "kilo.jsonc"), filepath.Join(xdgConfigDir(home), "kilo", "config.json"))
		},
		ProjectPath: func(root string) string {
			return firstExisting(filepath.Join(root, "kilo.json"), filepath.Join(root, ".kilo", "kilo.json"), filepath.Join(root, "kilo.jsonc"))
		},
		DetectPaths:    []string{".config/kilo", ".kilo", ".kilocode"},
		DetectBinaries: []string{"kilo"},
		DocsURL:        "https://kilo.ai/docs/automate/mcp/using-in-cli",
		Note:           "Key is \"mcp\" and entries use {type:local, command:[argv]}.",
	},
	{
		ID: "kimi-code-cli", Name: "Kimi Code CLI", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			if v := os.Getenv("KIMI_CODE_HOME"); v != "" {
				return filepath.Join(v, "mcp.json")
			}
			return filepath.Join(home, ".kimi-code", "mcp.json")
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".kimi-code", "mcp.json") },
		DetectPaths:    []string{".kimi-code", ".kimi"},
		DetectBinaries: []string{"kimi"},
		DocsURL:        "https://kimi.com/code/docs/en/kimi-code-cli/customization/mcp.html",
		Note:           "Directory is .kimi-code, not .kimi.",
	},
	{
		ID: "kiro-cli", Name: "Kiro CLI", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			// Kiro publishes two path sets for the same file; probe both.
			return firstExisting(
				filepath.Join(home, ".kiro", "settings", "mcp.json"),
				filepath.Join(home, ".kiro", "mcp.json"))
		},
		ProjectPath: func(root string) string {
			return firstExisting(
				filepath.Join(root, ".kiro", "settings", "mcp.json"),
				filepath.Join(root, ".kiro", "mcp.json"))
		},
		DetectPaths:    []string{".kiro"},
		DetectBinaries: []string{"kiro-cli"},
		DocsURL:        "https://kiro.dev/docs/mcp/configuration",
		Note:           "CLI registry mode documents a different, flatter path set; we target the general settings path.",
	},
	{
		ID: "kode", Name: "Kode", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			if v := os.Getenv("KODE_CONFIG_DIR"); v != "" {
				return filepath.Join(v, "kode.json")
			}
			return filepath.Join(home, ".kode.json")
		},
		ProjectPath: func(root string) string {
			return firstExisting(filepath.Join(root, ".mcp.json"), filepath.Join(root, ".mcprc"))
		},
		DetectPaths:    []string{".kode"},
		DetectBinaries: []string{"kode"},
		DocsURL:        "https://github.com/shareAI-lab/Kode-cli",
		Note:           "Project file .mcp.json; global map lives in ~/.kode.json.",
	},
	{
		ID: "crush", Name: "Crush", Format: FormatJSON,
		UserKey: []string{"mcp"}, ProjectKey: []string{"mcp"},
		// Crush's published schema requires `type`, and its decoder applies no
		// default, so an entry without it starts no transport at all.
		Shape: ShapeStdioTyped,
		UserPath: func(home string) string {
			if v := os.Getenv("CRUSH_GLOBAL_CONFIG"); v != "" {
				return filepath.Join(v, "crush.json")
			}
			if ad := windowsLocalAppData(); ad != "" {
				// Crush documents %LOCALAPPDATA%\crush\crush.json for the Windows
				// user-global file; %APPDATA% is not read.
				return filepath.Join(ad, "crush", "crush.json")
			}
			return filepath.Join(xdgConfigDir(home), "crush", "crush.json")
		},
		ProjectPath: func(root string) string {
			return firstExisting(filepath.Join(root, ".crush.json"), filepath.Join(root, "crush.json"))
		},
		DetectPaths:    []string{".config/crush"},
		DetectBinaries: []string{"crush"},
		DocsURL:        "https://charmbracelet-crush.mintlify.app/configuration/mcp",
		Note:           "Key is \"mcp\" and entries require an explicit \"type\" (stdio|http|sse); JSON is the legacy-but-machine-readable format.",
	},
	{
		ID: "zcode", Name: "ZCode", Format: FormatJSON,
		UserKey: []string{"mcp", "servers"}, ProjectKey: []string{"mcp", "servers"},
		Shape:       ShapeObject,
		UserPath:    func(home string) string { return filepath.Join(home, ".zcode", "cli", "config.json") },
		ProjectPath: func(root string) string { return filepath.Join(root, ".zcode", "config.json") },
		DetectPaths: []string{".zcode"},
		DocsURL:     "https://zcode.z.ai/en/docs/mcp-services",
		Note:        "Nested mcp.servers. A .zcode config fully suppresses a .agents config in the same scope.",
	},
	{
		ID: "zed", Name: "Zed", Format: FormatJSON,
		UserKey: []string{"context_servers"}, ProjectKey: []string{"context_servers"},
		Shape: ShapeObject, TolerateComments: true,
		UserPath: func(home string) string {
			if ad := windowsAppData(); ad != "" {
				return filepath.Join(ad, "Zed", "settings.json")
			}
			return filepath.Join(xdgConfigDir(home), "zed", "settings.json")
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".zed", "settings.json") },
		DetectPaths:    []string{".config/zed", ".local/share/zed"},
		DetectBinaries: []string{"zed"},
		DocsURL:        "https://zed.dev/docs/ai/mcp",
		Note:           "Key is \"context_servers\" (NOT mcpServers). settings.json is JSONC; comments are preserved on write.",
	},
	{
		ID: "bob", Name: "IBM Bob", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:       ShapeObject,
		UserPath:    func(home string) string { return filepath.Join(home, ".bob", "settings", "mcp.json") },
		ProjectPath: func(root string) string { return filepath.Join(root, ".bob", "mcp.json") },
		DetectPaths: []string{".bob"},
		DocsURL:     "https://bob.ibm.com/docs/ide/configuration/mcp/mcp-in-bob",
		Note:        "Project overrides global on name conflict. No documented env override for the settings dir.",
	},
	{
		ID: "codearts-agent", Name: "CodeArts Agent", Format: FormatJSON,
		UserKey: []string{"mcp"}, ProjectKey: []string{"mcp"},
		Shape: ShapeLocalArray, TolerateComments: true,
		UserPath: func(home string) string {
			return firstExisting(filepath.Join(home, ".codeartsdoer", "codearts_cli.jsonc"), filepath.Join(home, ".codeartsdoer", "codearts_cli.json"))
		},
		ProjectPath: func(root string) string {
			return firstExisting(
				filepath.Join(root, ".codeartsdoer", "codearts_cli.jsonc"),
				filepath.Join(root, ".codeartsdoer", "codearts_cli.json"))
		},
		DetectPaths: []string{".codeartsdoer"},
		DocsURL:     "https://support.huaweicloud.com/intl/en-us/usermanual-cli/codeartsagent_cli_0035.html",
		Note:        "Three deviations from the common shape: key is \"mcp\", command is an argv ARRAY, and the env key is \"environment\".",
	},
	{
		ID: "codebuddy", Name: "CodeBuddy", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject, TolerateComments: true,
		UserPath: func(home string) string {
			// Documented priority: .mcp.json, then mcp.json (deprecated), then the
			// legacy ~/.codebuddy.json. Skipping the middle candidate made an
			// existing mcp.json invisible to us, so the bridge was written to a
			// file CodeBuddy never consults for MCP.
			return firstExisting(
				filepath.Join(home, ".codebuddy", ".mcp.json"),
				filepath.Join(home, ".codebuddy", "mcp.json"),
				filepath.Join(home, ".codebuddy.json"))
		},
		ProjectPath: func(root string) string {
			return firstExisting(filepath.Join(root, ".mcp.json"), filepath.Join(root, "mcp.json"))
		},
		DetectPaths:    []string{".codebuddy"},
		DetectBinaries: []string{"codebuddy", "cbc"},
		DocsURL:        "https://codebuddy.ai/docs/cli/mcp",
		Note:           "JSONC. Read order: .mcp.json -> mcp.json (deprecated) -> ~/.codebuddy.json (legacy).",
	},
	{
		// The vendor's documented .codestudio/mcp.json sample contains // comments.
		ID: "codestudio", Name: "Code Studio", Format: FormatJSON, TolerateComments: true,
		ProjectKey:  []string{"servers"},
		Shape:       ShapeObject,
		ProjectPath: func(root string) string { return filepath.Join(root, ".codestudio", "mcp.json") },
		DetectPaths: []string{".codestudio"},
		DocsURL:     "https://ej2.syncfusion.com/angular/documentation/mcp",
		Note:        "Workspace-only (no user-scope path is documented) and the key is \"servers\", not mcpServers.",
	},
	{
		ID: "command-code", Name: "Command Code", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".commandcode", "mcp.json") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".mcp.json") },
		DetectPaths:    []string{".commandcode"},
		DetectBinaries: []string{"cmd", "cmdc", "command-code"},
		DocsURL:        "https://commandcode.ai/docs/mcp",
		Note:           "Three scopes: local (projects/<slug>/mcp.json) > project > user. OAuth secrets are stripped into mcp-tokens.json.",
	},
	{
		ID: "cortex", Name: "Cortex Code", Format: FormatJSON,
		UserKey:        []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".snowflake", "cortex", "mcp.json") },
		DetectPaths:    []string{".snowflake/cortex"},
		DetectBinaries: []string{"cortex"},
		DocsURL:        "https://docs.snowflake.com/en/user-guide/cortex-code/cortex-code-mcp",
		Note:           "Credentials are migrated to the OS keychain on first connect and stripped from mcp.json.",
	},
	{
		ID: "openhands", Name: "OpenHands", Format: FormatJSON,
		UserKey: []string{"mcpServers"},
		Shape:   ShapeObject,
		UserPath: func(home string) string {
			if v := os.Getenv("OH_PERSISTENCE_DIR"); v != "" {
				return filepath.Join(v, "mcp.json")
			}
			return filepath.Join(home, ".openhands", "mcp.json")
		},
		DetectPaths:    []string{".openhands"},
		DetectBinaries: []string{"openhands"},
		DocsURL:        "https://docs.openhands.dev/overview/model-context-protocol",
		Note:           "Current releases IGNORE legacy config.toml [mcp]; JSON replaced TOML at 1.0.0.",
	},
	{
		ID: "pochi", Name: "Pochi", Format: FormatJSON,
		UserKey: []string{"mcp"}, ProjectKey: []string{"mcp"},
		Shape: ShapeObject, TolerateComments: true,
		UserPath:       func(home string) string { return filepath.Join(home, ".pochi", "config.jsonc") },
		ProjectPath:    func(root string) string { return filepath.Join(root, ".pochi", "config.jsonc") },
		DetectPaths:    []string{".pochi"},
		DetectBinaries: []string{"pochi"},
		DocsURL:        "https://docs.getpochi.com/mcp",
		Note:           "Key is \"mcp\", not mcpServers. JSONC.",
	},
	{
		ID: "posit-assistant", Name: "Posit Assistant", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:       ShapeLocalArray,
		UserPath:    func(home string) string { return filepath.Join(home, ".posit", "assistant", "settings.json") },
		ProjectPath: func(root string) string { return filepath.Join(root, ".posit", "assistant", "settings.json") },
		DetectPaths: []string{".posit/assistant", ".positai"},
		DocsURL:     "https://assistant.posit.co/docs/reference/mcp-servers/",
		Note:        "Product-level file, NOT the IDE's settings.json. Entries use an argv command array and the \"environment\" env key.",
	},
	{
		ID: "qoder", Name: "Qoder", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject, TolerateComments: true,
		UserPath: func(home string) string {
			if v := os.Getenv("QODER_CONFIG_DIR"); v != "" {
				return filepath.Join(v, "settings.json")
			}
			return filepath.Join(home, ".qoder", "settings.json")
		},
		ProjectPath: func(root string) string {
			return firstExisting(filepath.Join(root, ".qoder", "settings.json"), filepath.Join(root, ".qoder", "settings.local.json"), filepath.Join(root, ".mcp.json"))
		},
		DetectPaths:    []string{".qoder"},
		DetectBinaries: []string{"qoder"},
		DocsURL:        "https://docs.qoder.com/cli/mcp-servers",
		Note:           "Targets the Qoder CLI. The Qoder IDE stores MCP separately and is not covered.",
	},
	{
		ID: "qoder-cn", Name: "Qoder CN", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject, TolerateComments: true,
		UserPath: func(home string) string {
			if v := os.Getenv("QODERCN_CONFIG_DIR"); v != "" {
				return filepath.Join(v, "settings.json")
			}
			return filepath.Join(home, ".qoder-cn", "settings.json")
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".qoder", "settings.json") },
		DetectPaths:    []string{".qoder-cn"},
		DetectBinaries: []string{"qodercn"},
		DocsURL:        "https://docs.qoder.cn/cli/mcp-reference",
		Note:           "Separate product. User dir .qoder-cn and env QODERCN_CONFIG_DIR (no underscore), but project paths still use .qoder.",
	},
	{
		ID: "qwen-code", Name: "Qwen Code", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape: ShapeObject,
		UserPath: func(home string) string {
			// QWEN_HOME customizes the global configuration directory.
			if dir := os.Getenv("QWEN_HOME"); dir != "" {
				return filepath.Join(dir, ".qwen", "settings.json")
			}
			return filepath.Join(home, ".qwen", "settings.json")
		},
		ProjectPath:    func(root string) string { return filepath.Join(root, ".qwen", "settings.json") },
		DetectPaths:    []string{".qwen"},
		DetectBinaries: []string{"qwen"},
		DocsURL:        "https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/mcp.md",
		Note:           "Forks the Gemini CLI layout. Remote HTTP uses \"httpUrl\"; \"url\" is legacy SSE only.",
	},
	{
		ID: "ona", Name: "Ona", Format: FormatJSON,
		ProjectKey:  []string{"mcpServers"},
		Shape:       ShapeObject,
		ProjectPath: func(root string) string { return filepath.Join(root, ".ona", "mcp-config.json") },
		DetectPaths: []string{".ona"},
		DocsURL:     "https://ona.com/docs/ona/mcp",
		Note:        "Repo-local file only; Ona has no documented user-scope MCP config. A cloud platform, not a local CLI.",
	},
	{
		ID: "neovate", Name: "Neovate", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:    ShapeObject,
		UserPath: func(home string) string { return filepath.Join(home, ".neovate", "config.json") },
		ProjectPath: func(root string) string {
			return firstExisting(filepath.Join(root, ".neovate", "config.json"), filepath.Join(root, ".neovate", "config.local.json"))
		},
		DetectPaths:    []string{".neovate"},
		DetectBinaries: []string{"neovate"},
		DocsURL:        "https://github.com/neovateai/neovate-code",
		Note:           "Verified from the product's own source (src/config.ts, src/mcp.ts) rather than docs, which were unreachable. Entries discriminate on a \"type\" field.",
	},
	{
		ID: "mux", Name: "Mux (Xum)", Format: FormatJSON,
		UserKey: []string{"servers"}, ProjectKey: []string{"servers"},
		Shape: ShapeCommandString, TolerateComments: true,
		UserPath:    func(home string) string { return filepath.Join(home, ".xum", "mcp.jsonc") },
		ProjectPath: func(root string) string { return filepath.Join(root, ".xum", "mcp.jsonc") },
		DetectPaths: []string{".xum", ".mux"},
		DocsURL:     "https://xum.coder.com/config/mcp-servers",
		Note:        "Renamed from Mux to Xum. Each server maps a name straight to one stdio command LINE, so there is no wrapper object and no per-server metadata. Stdio only.",
	},
	{
		ID: "rovodev", Name: "Rovo Dev", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:          ShapeObject,
		UserPath:       func(home string) string { return filepath.Join(home, ".rovodev", "mcp.json") },
		DetectPaths:    []string{".rovodev"},
		DetectBinaries: []string{"acli"},
		DocsURL:        "https://support.atlassian.com/rovo/docs/connect-to-an-mcp-server-in-rovo-dev-cli/",
		Note:           "Clean single-file, single-key config. config.yml only points at this file and carries an allowlist we deliberately do not touch.",
	},
	{
		ID: "tabnine-cli", Name: "Tabnine CLI", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, ProjectKey: []string{"mcpServers"},
		Shape:    ShapeObject,
		UserPath: func(home string) string { return filepath.Join(home, ".tabnine", "agent", "settings.json") },
		// The vendor documents a workspace settings file with the same shape.
		ProjectPath:    func(root string) string { return filepath.Join(root, ".tabnine", "agent", "settings.json") },
		DetectPaths:    []string{".tabnine"},
		DetectBinaries: []string{"tabnine"},
		DocsURL:        "https://docs.tabnine.com/main/getting-started/tabnine-agent/mcp-intro-and-setup/mcp-server-config",
		Note:           "mcpServers lives inside a general settings document shared by many features, so only that key is touched. The product is in maintenance mode and deprecated after 2026-12-31.",
	},
	{
		ID: "roo", Name: "Roo Code", Format: FormatJSON,
		ProjectKey:  []string{"mcpServers"},
		Shape:       ShapeObject,
		ProjectPath: func(root string) string { return filepath.Join(root, ".roo", "mcp.json") },
		DetectPaths: []string{".roo"},
		DocsURL:     "https://docs.roocode.com/features/mcp/using-mcp-in-roo",
		Note:        "Repo-scope only. The IDE stores user-scope servers under VS Code's globalStorage, whose absolute path the vendor docs never print, so we do not guess it.",
	},
}

// LookupBridgeTarget returns a verified target by host id.
func LookupBridgeTarget(id string) (BridgeTarget, bool) {
	for _, t := range verifiedBridgeTargets {
		if t.ID == id {
			return t, true
		}
	}
	return BridgeTarget{}, false
}

// VerifiedBridgeTargets returns every compiled, evidence-backed target.
func VerifiedBridgeTargets() []BridgeTarget {
	out := make([]BridgeTarget, len(verifiedBridgeTargets))
	copy(out, verifiedBridgeTargets)
	return out
}
