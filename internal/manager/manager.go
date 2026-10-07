// Package manager plans installations through ecosystem package managers
// (npm, uv, pipx, cargo, brew, winget, docker). It produces ordered,
// inspectable step plans; execution is opt-in (EnableExec) and bounded, and
// never runs package scripts implicitly.
package manager

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Tool identifies an ecosystem installer.
type Tool string

const (
	ToolNPM    Tool = "npm"
	ToolUV     Tool = "uv"
	ToolPipx   Tool = "pipx"
	ToolCargo  Tool = "cargo"
	ToolBrew   Tool = "brew"
	ToolWinget Tool = "winget"
	ToolDocker Tool = "docker"
)

// Step is one planned installer invocation.
type Step struct {
	Tool Tool     `json:"tool"`
	Args []string `json:"args"`
	// Reason explains why the step exists (auditability).
	Reason string `json:"reason"`
}

// Plan is an ordered install plan.
type Plan struct {
	Package string `json:"package"`
	Version string `json:"version,omitempty"`
	Steps   []Step `json:"steps"`
}

// For maps a package ecosystem hint to a tool. Unknown ecosystems fail
// closed rather than guessing an installer.
func For(ecosystem string) (Tool, error) {
	switch strings.ToLower(strings.TrimSpace(ecosystem)) {
	case "npm", "node", "npx":
		return ToolNPM, nil
	case "pypi", "python", "uv", "uvx":
		return ToolUV, nil
	case "pipx":
		return ToolPipx, nil
	case "cargo", "crates", "rust":
		return ToolCargo, nil
	case "brew", "homebrew":
		return ToolBrew, nil
	case "winget":
		return ToolWinget, nil
	case "oci", "docker":
		return ToolDocker, nil
	default:
		return "", fmt.Errorf("LPSM-MANAGER-UNKNOWN-ECOSYSTEM: %q", ecosystem)
	}
}

// PlanInstall builds the ordered steps for ecosystem + package@version.
// Install scripts are never enabled: npm uses --ignore-scripts, pip uses
// --no-deps-style isolation where applicable, cargo uses --locked.
func PlanInstall(ecosystem, pkg, version string) (Plan, error) {
	tool, err := For(ecosystem)
	if err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(pkg) == "" {
		return Plan{}, fmt.Errorf("LPSM-MANAGER-PACKAGE: package name is required")
	}
	ref := pkg
	if version != "" {
		ref = pkg + "@" + version
	}
	var steps []Step
	switch tool {
	case ToolNPM:
		steps = []Step{{Tool: ToolNPM, Args: []string{"install", "--global", "--ignore-scripts", ref}, Reason: "global install without lifecycle scripts"}}
	case ToolUV:
		steps = []Step{{Tool: ToolUV, Args: []string{"tool", "install", ref}, Reason: "isolated tool install"}}
	case ToolPipx:
		steps = []Step{{Tool: ToolPipx, Args: []string{"install", ref}, Reason: "isolated pipx install"}}
	case ToolCargo:
		args := []string{"install", "--locked", pkg}
		if version != "" {
			args = append(args, "--version", version)
		}
		steps = []Step{{Tool: ToolCargo, Args: args, Reason: "locked cargo install"}}
	case ToolBrew:
		steps = []Step{{Tool: ToolBrew, Args: []string{"install", ref}, Reason: "brew install"}}
	case ToolWinget:
		args := []string{"install", pkg}
		if version != "" {
			args = append(args, "--version", version)
		}
		steps = []Step{{Tool: ToolWinget, Args: args, Reason: "winget install"}}
	case ToolDocker:
		steps = []Step{{Tool: ToolDocker, Args: []string{"pull", ref}, Reason: "digest-pinned image pull (pin enforced by runtime.OCIAdapter)"}}
	}
	return Plan{Package: pkg, Version: version, Steps: steps}, nil
}

// Executor runs planned steps. Exec is disabled by default: Execute fails
// closed unless EnableExec is true, so plans stay inspectable in tests, CI
// and previews.
type Executor struct {
	EnableExec bool
	// Runner executes one step; nil means refuse (used by tests to capture).
	Runner func(ctx context.Context, s Step) error
}

// Execute runs the plan in order, stopping at the first failure.
func (e *Executor) Execute(ctx context.Context, p Plan) error {
	if !e.EnableExec {
		return fmt.Errorf("LPSM-MANAGER-EXEC-DISABLED: refusing to execute %d step(s) for %q without explicit opt-in", len(p.Steps), p.Package)
	}
	if e.Runner == nil {
		return fmt.Errorf("LPSM-MANAGER-NO-RUNNER: no step runner configured")
	}
	for i, s := range p.Steps {
		if err := e.Runner(ctx, s); err != nil {
			return fmt.Errorf("LPSM-MANAGER-STEP-FAILED: step %d (%s %s): %w", i, s.Tool, strings.Join(s.Args, " "), err)
		}
	}
	return nil
}

// Supported returns the tool list in stable order.
func Supported() []Tool {
	out := []Tool{ToolNPM, ToolUV, ToolPipx, ToolCargo, ToolBrew, ToolWinget, ToolDocker}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
