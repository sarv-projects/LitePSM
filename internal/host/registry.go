package host

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

var (
	registryMu sync.RWMutex
	adapters   = map[string]HostAdapter{
		"codex":       &CodexAdapter{},
		"claude-code": &ClaudeCodeAdapter{},
		"opencode":    &OpenCodeAdapter{},
		"cline":       &ClineAdapter{},
		"pi-agent":    &PiAgentAdapter{},
		"pi":          &PiAgentAdapter{},
		"grok":        &GrokBuildAdapter{},
		"grok-build":  &GrokBuildAdapter{},
	}
)

// RegisterAdapter registers or overrides a HostAdapter in the global registry.
func RegisterAdapter(hostID string, adapter HostAdapter) {
	registryMu.Lock()
	defer registryMu.Unlock()
	adapters[strings.ToLower(hostID)] = adapter
}

// GetAdapter retrieves a HostAdapter by host ID.
func GetAdapter(hostID string) (HostAdapter, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	adapter, exists := adapters[strings.ToLower(hostID)]
	if !exists {
		return nil, fmt.Errorf("unknown host adapter %q", hostID)
	}
	return adapter, nil
}

// ListAdapters returns a slice of all distinct registered HostAdapters.
func ListAdapters() []HostAdapter {
	registryMu.RLock()
	defer registryMu.RUnlock()

	seen := make(map[string]bool)
	var list []HostAdapter
	for _, a := range adapters {
		id := a.Descriptor().HostID
		if !seen[id] {
			seen[id] = true
			list = append(list, a)
		}
	}
	return list
}

// DetectInstalledHosts checks verification and configuration status across all registered adapters.
func DetectInstalledHosts(ctx context.Context) ([]HostVerification, error) {
	all := ListAdapters()
	var results []HostVerification
	for _, a := range all {
		v, err := a.VerifySetup(ctx)
		if err != nil {
			results = append(results, HostVerification{
				HostID: a.Descriptor().HostID,
				Status: "error",
			})
			continue
		}
		results = append(results, *v)
	}
	return results, nil
}
