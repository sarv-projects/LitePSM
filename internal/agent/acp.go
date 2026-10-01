package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
)

// ACPAdapter resolves ACP-registry agents into concrete launch specifications.
// It is data-driven: adding an agent to the registry requires no code change.
type ACPAdapter struct{}

// NewACPAdapter creates an ACP agent adapter.
func NewACPAdapter() *ACPAdapter { return &ACPAdapter{} }

// SourceID identifies the ACP registry source.
func (a *ACPAdapter) SourceID() string { return "builtin:acp-registry" }

// ParseRegistry decodes an ACP registry index document.
func ParseRegistry(data []byte) (*Registry, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("acp registry is empty")
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("failed to parse ACP registry: %w", err)
	}
	if len(reg.Agents) == 0 {
		return nil, fmt.Errorf("ACP registry contains no agents")
	}
	return &reg, nil
}

// MatchTarget maps a Go GOOS/GOARCH pair onto an ACP distribution target.
func MatchTarget(goos, goarch string) (Target, bool) {
	switch goos {
	case "darwin":
		if goarch == "arm64" {
			return TargetDarwinARM64, true
		}
		return TargetDarwinAMD64, true
	case "linux":
		if goarch == "arm64" {
			return TargetLinuxARM64, true
		}
		return TargetLinuxAMD64, true
	case "windows":
		if goarch == "arm64" {
			return TargetWindowsARM64, true
		}
		return TargetWindowsAMD64, true
	}
	return "", false
}

// HostTarget returns the ACP target for the current host.
func HostTarget() (Target, bool) {
	return MatchTarget(runtime.GOOS, runtime.GOARCH)
}

// Supports reports whether the agent has any resolvable distribution.
func (a *ACPAdapter) Supports(ag Agent) bool {
	d := ag.Distribution
	return d.Npx != nil || d.Uvx != nil || len(d.Binary) > 0
}

// Resolve builds a launch specification for the agent on the given target.
// Resolution order is npx, then uvx, then a platform binary. binaryDir is the
// directory where a binary distribution has been (or will be) extracted.
func (a *ACPAdapter) Resolve(ag Agent, target Target, binaryDir string) (*LaunchSpec, error) {
	d := ag.Distribution

	if d.Npx != nil && d.Npx.Package != "" {
		exe := "npx"
		if runtime.GOOS == "windows" {
			exe = "npx.cmd"
		}
		args := append([]string{"-y", d.Npx.Package}, d.Npx.Args...)
		return &LaunchSpec{
			AgentID:    ag.ID,
			Strategy:   "npx",
			Executable: exe,
			Args:       args,
			Env:        d.Npx.Env,
		}, nil
	}

	if d.Uvx != nil && d.Uvx.Package != "" {
		args := append([]string{d.Uvx.Package}, d.Uvx.Args...)
		return &LaunchSpec{
			AgentID:    ag.ID,
			Strategy:   "uvx",
			Executable: "uvx",
			Args:       args,
			Env:        d.Uvx.Env,
		}, nil
	}

	if len(d.Binary) > 0 {
		bt, ok := d.Binary[target]
		if !ok {
			return nil, fmt.Errorf("agent %s has no binary distribution for target %s", ag.ID, target)
		}
		return &LaunchSpec{
			AgentID:    ag.ID,
			Strategy:   "binary",
			Executable: filepath.Join(binaryDir, bt.Cmd),
			Args:       bt.Args,
			Env:        bt.Env,
			Archive:    bt.Archive,
			SHA256:     bt.SHA256,
		}, nil
	}

	return nil, fmt.Errorf("agent %s has no supported distribution", ag.ID)
}
