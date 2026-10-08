package source

import (
	"github.com/sarv-projects/litespm/internal/domain"
)

// sources.go — the checked-in registry of upstream sources.
//
// This is the "add to registry" surface: every upstream marketplace LiteSPM
// knows how to ingest is listed here with its canonical SourceID, publisher,
// repository, manifest path(s), and manifest format. Adapters use it as a
// fallback for publisher attribution when a manifest carries no owner.

// SourceDef describes one known upstream source.
type SourceDef struct {
	// ID is the canonical <namespace>:<slug> source identifier.
	ID domain.SourceID
	// Name is the human-readable source name.
	Name string
	// Publisher is the vendor or community claiming the source.
	Publisher string
	// RepoURL is the upstream Git repository.
	RepoURL string
	// ManifestPaths lists manifest files relative to the repository root.
	ManifestPaths []string
	// Format names the manifest shape the adapters parse.
	Format string
}

// KnownSources is the full registry of upstream sources.
var KnownSources = []SourceDef{
	{
		ID:        domain.SourceID("builtin:mcp-registry"),
		Name:      "Official MCP Registry",
		Publisher: "Model Context Protocol",
		Format:    "server.json",
	},
	{
		ID:        domain.SourceID("builtin:agent-skills"),
		Name:      "Agent Skills",
		Publisher: "agentskills.io",
		Format:    "skill-md",
	},
	{
		ID:        domain.SourceID("builtin:claude-plugins"),
		Name:      "Claude marketplace (generic)",
		Publisher: "Community",
		Format:    "claude-marketplace.json",
	},
	{
		ID:        domain.SourceID("builtin:openai-plugins"),
		Name:      "OpenAI portable plugins (generic)",
		Publisher: "Community",
		Format:    "plugin.json",
	},
	{
		ID:        domain.SourceID("builtin:grok-plugins"),
		Name:      "Grok marketplace (generic)",
		Publisher: "Community",
		Format:    "grok-marketplace.json",
	},
	{
		ID:            domain.SourceID("git:anthropics-skills"),
		Name:          "Anthropic Agent Skills",
		Publisher:     "Anthropic",
		RepoURL:       "https://github.com/anthropics/skills",
		ManifestPaths: []string{".claude-plugin/marketplace.json"},
		Format:        "claude-marketplace.json",
	},
	{
		ID:            domain.SourceID("git:claude-plugins-official"),
		Name:          "Claude Code Plugins Directory",
		Publisher:     "Anthropic",
		RepoURL:       "https://github.com/anthropics/claude-plugins-official",
		ManifestPaths: []string{".claude-plugin/marketplace.json"},
		Format:        "claude-marketplace.json",
	},
	{
		ID:            domain.SourceID("git:knowledge-work-plugins"),
		Name:          "Knowledge Work Plugins",
		Publisher:     "Anthropic",
		RepoURL:       "https://github.com/anthropics/knowledge-work-plugins",
		ManifestPaths: []string{".claude-plugin/marketplace.json"},
		Format:        "claude-marketplace.json",
	},
	{
		ID:        domain.SourceID("git:openai-plugins"),
		Name:      "OpenAI Codex Plugins",
		Publisher: "OpenAI",
		RepoURL:   "https://github.com/openai/plugins",
		ManifestPaths: []string{
			".agents/plugins/marketplace.json",
			".agents/plugins/api_marketplace.json",
		},
		Format: "codex-marketplace.json",
	},
	{
		ID:            domain.SourceID("git:cursor-plugins"),
		Name:          "Cursor Plugins",
		Publisher:     "Cursor",
		RepoURL:       "https://github.com/cursor/plugins",
		ManifestPaths: []string{".cursor-plugin/marketplace.json"},
		Format:        "cursor-marketplace.json",
	},
	{
		ID:            domain.SourceID("git:xai-plugin-marketplace"),
		Name:          "xAI Plugin Marketplace",
		Publisher:     "xAI",
		RepoURL:       "https://github.com/xai-org/plugin-marketplace",
		ManifestPaths: []string{".grok-plugin/marketplace.json"},
		Format:        "grok-marketplace.json",
	},
	// The directory sources are walked by the dataset producer
	// (scripts/build_full_catalog.py): sitemap in, one record per
	// capability. They carry no git manifest paths -- skills.sh serves
	// SKILL.md frontmatter through its download API, mcpservers.org page
	// metadata through public Wayback Machine replays (its own API paths
	// are robots-disallowed), and mcpmarket.com serves its own listing
	// pages directly (robots.txt allows them and sets Crawl-delay: 1;
	// only /api/ is disallowed). RepoURL stays empty because none of them
	// is ingested from a git repository.
	{
		ID:        domain.SourceID("feed:skills-sh"),
		Name:      "skills.sh directory",
		Publisher: "skills.sh",
		Format:    "skill-md",
	},
	{
		ID:        domain.SourceID("feed:mcpservers-org"),
		Name:      "MCPServers.org directory",
		Publisher: "mcpservers.org",
		Format:    "directory-html",
	},
	{
		ID:        domain.SourceID("feed:mcpmarket-com"),
		Name:      "MCP Market directory",
		Publisher: "mcpmarket.com",
		Format:    "directory-html",
	},
}

// LookupSource returns the registry entry for a SourceID, if known.
func LookupSource(id domain.SourceID) (SourceDef, bool) {
	for _, s := range KnownSources {
		if s.ID == id {
			return s, true
		}
	}
	return SourceDef{}, false
}

// PublisherForSource returns the registry publisher for a SourceID, or "".
func PublisherForSource(id domain.SourceID) string {
	if s, ok := LookupSource(id); ok {
		return s.Publisher
	}
	return ""
}
