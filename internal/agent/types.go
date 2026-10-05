// Package agent implements LiteSPM's agent-layer adapters: resolving and
// launching installable agents (starting with the Agent Client Protocol, ACP)
// as distinct from internal/host, which injects bridge entries into an already
// installed agent's configuration.
package agent

// Target identifies a supported OS/architecture distribution target used by the
// ACP registry.
type Target string

const (
	TargetDarwinARM64  Target = "darwin-aarch64"
	TargetDarwinAMD64  Target = "darwin-x86_64"
	TargetLinuxARM64   Target = "linux-aarch64"
	TargetLinuxAMD64   Target = "linux-x86_64"
	TargetWindowsARM64 Target = "windows-aarch64"
	TargetWindowsAMD64 Target = "windows-x86_64"
)

// PackageDistribution describes an npx- or uvx-based agent distribution.
type PackageDistribution struct {
	Package string            `json:"package"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// BinaryTarget describes a per-platform prebuilt agent archive.
type BinaryTarget struct {
	Archive string            `json:"archive"`
	Cmd     string            `json:"cmd"`
	Args    []string          `json:"args,omitempty"`
	SHA256  string            `json:"sha256,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Distribution is the set of ways an agent can be installed.
type Distribution struct {
	Npx    *PackageDistribution    `json:"npx,omitempty"`
	Uvx    *PackageDistribution    `json:"uvx,omitempty"`
	Binary map[Target]BinaryTarget `json:"binary,omitempty"`
}

// Agent is a single installable agent entry from a registry.
type Agent struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description,omitempty"`
	Repository   string       `json:"repository,omitempty"`
	Website      string       `json:"website,omitempty"`
	Icon         string       `json:"icon,omitempty"`
	License      string       `json:"license,omitempty"`
	Authors      []string     `json:"authors,omitempty"`
	Distribution Distribution `json:"distribution"`
}

// Registry is the ACP registry index document.
type Registry struct {
	Version string  `json:"version"`
	Agents  []Agent `json:"agents"`
}

// LaunchSpec is a fully resolved, executable agent launch description.
// Archive and SHA256 are set only for binary distributions. Notes carries
// verified caveats from the override layer; Deprecated marks retired agents.
type LaunchSpec struct {
	AgentID    string
	Strategy   string // "npx" | "uvx" | "binary"
	Executable string
	Args       []string
	Env        map[string]string
	Archive    string
	SHA256     string
	Notes      []string
	Deprecated bool
}
