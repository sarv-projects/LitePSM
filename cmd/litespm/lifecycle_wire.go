package main

import (
	"context"
	"strings"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/lifecycle"
	"github.com/sarv-projects/litespm/internal/state"
)

// newLifecycleOrchestrator returns the install/remove coordinator.
func newLifecycleOrchestrator(db *state.DB) *lifecycle.Orchestrator {
	if db == nil {
		return nil
	}
	return lifecycle.New(db)
}

// hostStructureTypeFor maps a host config format onto the ARCH/33 §3
// structure type vocabulary.
func hostStructureTypeFor(hostID string) string {
	adapter, err := host.GetAdapter(hostID)
	if err != nil {
		return "json-object"
	}
	switch strings.ToLower(adapter.Descriptor().ConfigFormat) {
	case "toml":
		return "toml-table"
	case "yaml", "yml":
		return "yaml-path"
	default:
		return "json-object"
	}
}

// hostLocatorFor builds the ARCH/33 §3 in-structure address of an owned MCP
// entry (e.g. mcpServers.demo-mcp).
func hostLocatorFor(hostID, entryName string) string {
	containers := map[string]string{
		"claude-code": "mcpServers",
		"cline":       "mcpServers",
		"pi-agent":    "mcpServers",
		"pi":          "mcpServers",
		"opencode":    "mcp",
		"codex":       "mcp_servers",
		"grok":        "mcp_servers",
		"grok-build":  "mcp_servers",
	}
	if c, ok := containers[strings.ToLower(hostID)]; ok {
		return c + "." + entryName
	}
	return "mcpServers." + entryName
}

// removeMCPInstall performs the symmetric, ledger-driven remove for an MCP
// install (ARCH/33 §5): reconcile each owned locator three-way, strip only
// nodes still owned, detach user-edited nodes, then delete state.
func removeMCPInstall(ctx context.Context, db *state.DB, dataRoot string, installID string, scope domain.InstallScope) error {
	orch := newLifecycleOrchestrator(db)
	if orch == nil {
		return db.DeleteInstall(ctx, installID)
	}
	paths, _ := config.ResolvePlatformPaths()
	backupDir := dataRoot
	if paths != nil {
		backupDir = paths.BackupsPath()
		if dataRoot != "" {
			backupDir = dataRoot + "/backups"
		}
	}
	plan, err := orch.PlanRemove(ctx, installID, func(m deployment.Mutation) string {
		return lifecycle.HashFile(m.FilePath)
	})
	if err != nil {
		return err
	}
	// No ledger rows (pre-ledger install): fall back to a best-effort
	// entry strip by install record, then state deletion.
	if len(plan.Remove) == 0 && len(plan.Detach) == 0 && len(plan.Missing) == 0 {
		return db.DeleteInstall(ctx, installID)
	}
	_, err = orch.RemoveInstall(ctx, installID,
		func(m deployment.Mutation) string {
			return lifecycle.HashFile(m.FilePath)
		},
		func(m deployment.Mutation) error {
			parts := strings.Split(m.Locator, ".")
			name := parts[len(parts)-1]
			var sc domain.InstallScope = scope
			if m.Scope == "project" {
				sc = domain.ScopeProject
			}
			res, rerr := host.RemoveServerEntry(ctx, m.HostID, name, sc, backupDir, m.PriorEntry)
			if rerr != nil {
				return rerr
			}
			_ = res
			return nil
		})
	return err
}
