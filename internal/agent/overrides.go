package agent

// launchOverride encodes verified corrections to ACP registry metadata.
//
// The registry is the discovery source of truth and already carries correct
// launch args for most agents. Overrides exist only where documentation checks
// found a concrete defect: a missing required flag, a package whose executable
// bin differs from the package name, or an agent the upstream has retired.
// Purely informational caveats live in Notes and do not change the launch.
type launchOverride struct {
	// Args replaces the distribution-provided args when non-nil.
	Args []string
	// Executable overrides the binary distribution command basename.
	Executable string
	// NpxBin selects a specific package bin: `npx -p <pkg> <bin> ...`.
	NpxBin string
	// Env is merged on top of the distribution environment.
	Env map[string]string
	// Deprecated marks an agent the upstream has retired or replaced.
	Deprecated bool
	// Notes are human-readable caveats surfaced by the CLI.
	Notes []string
}

var launchOverrides = map[string]launchOverride{
	// Registry ships no args, but the documented ACP trigger is `--acp`.
	"sigit": {
		Args:  []string{"--acp"},
		Notes: []string{"Documented ACP trigger is the --acp flag, not an `acp` subcommand."},
	},
	// Package bin is `mcode`, not the package id.
	"minimax-code": {
		NpxBin: "mcode",
		Notes:  []string{"Package bin is `mcode`; invoked via `npx -p @minimax-ai/code mcode acp`."},
	},
	// Bare --acp starts TCP/UI mode; the stdio server needs --stdio.
	"github-copilot-cli": {
		Args:  []string{"--acp", "--stdio"},
		Notes: []string{"Registry omits --stdio; bare --acp starts the TCP/UI mode, not the stdio server."},
	},
	// Retired upstream.
	"kimi": {
		Deprecated: true,
		Notes:      []string{"Upstream kimi-cli is archived; migrate to the Kimi Code CLI."},
	},
	"gemini": {
		Notes: []string{"Vendor superseded Gemini CLI for some tiers; treat as at-risk."},
	},
	"cortex-code": {
		Notes: []string{"Requires `-c <connection_name>` and a pre-authenticated Snowflake connection."},
	},
	"harn": {
		Notes: []string{"Documented launch requires a pipeline file argument (`serve acp <file.harn>`)."},
	},
	"autohand": {
		Env:   map[string]string{"AUTOHAND_PERMISSION_MODE": "external"},
		Notes: []string{"Requires the separate autohand CLI to be installed and configured."},
	},
	"agoragentic-acp": {
		Notes: []string{"Repository README marks the npm package as a legacy relay; verify before shipping."},
	},
	"antigravity-acp": {
		Notes: []string{"No official ACP mode is documented for the agy CLI; this is a standalone server binary."},
	},
	"crow-cli": {
		Notes: []string{"Documented install is Python tooling; a prebuilt binary release was not confirmed."},
	},
	"cursor": {
		Notes: []string{"CLI binary is named `agent`; launch args must include `acp`."},
	},
	"corust-agent": {
		Notes: []string{"Repository appears transferred; auth and config are undocumented."},
	},
	"claude-acp": {
		Notes: []string{"Requires Node >= 22 and Claude Agent authentication."},
	},
	"deepagents": {
		Notes: []string{"Requires ANTHROPIC_API_KEY."},
	},
	"factory-droid": {
		Notes: []string{"Registry uses `--output-format acp-daemon`; docs also mention `acp`. Verify at runtime."},
	},
	"goose": {
		Notes: []string{"Project moved to the Agentic AI Foundation; distribution and launch unchanged."},
	},
}

func overrideFor(id string) (launchOverride, bool) {
	ov, ok := launchOverrides[id]
	return ov, ok
}

// IsDeprecated reports whether an agent id is known to be retired upstream.
func IsDeprecated(id string) bool {
	ov, ok := launchOverrides[id]
	return ok && ov.Deprecated
}

func mergeEnv(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
