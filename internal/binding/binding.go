// Package binding implements the P6 one-bridge default (ARCH/26 §11.5,
// ARCH/31): managed MCP entries sit behind the single per-host bridge entry,
// and native per-package registration is opt-in only via an explicit
// binding.mode = "native" declaration.
//
// Plugins are multi-component bundles: each child component is routed by its
// own kind (skill, mcp, rule, hook, tool, lsp, agent); rules/hooks/LSP/
// connectors travel as components, never as top-level installs.
package binding

import (
	"fmt"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// Mode selects how a package is registered with a host.
type Mode string

const (
	// ModeManaged is the default: the capability is served through the
	// single LiteSPM bridge entry; no per-package native config is written.
	ModeManaged Mode = "managed"
	// ModeNative opts into direct native per-package registration for hosts
	// that document it. Requires explicit declaration.
	ModeNative Mode = "native"
)

// DefaultMode is the one-bridge default.
const DefaultMode = ModeManaged

// ParseMode validates a binding mode string. Empty means the default.
func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "managed":
		return ModeManaged, nil
	case "native":
		return ModeNative, nil
	default:
		return "", fmt.Errorf("LPSM-BINDING-MODE: %q must be managed|native", raw)
	}
}

// Request describes what should be bound where.
type Request struct {
	ListingID domain.ListingID `json:"listingId"`
	Mode      Mode             `json:"mode"`
	HostID    string           `json:"hostId"`
}

// Decision is the resolved binding.
type Decision struct {
	Mode        Mode   `json:"mode"`
	EntryStyle  string `json:"entryStyle"` // "bridge" | "native"
	Reason      string `json:"reason"`
	NeedsBridge bool   `json:"needsBridge"`
}

// Resolve applies the one-bridge default: managed unless the caller
// explicitly opted into native.
func Resolve(req Request) (Decision, error) {
	if strings.TrimSpace(string(req.ListingID)) == "" {
		return Decision{}, fmt.Errorf("LPSM-BINDING-ID: listing id is required")
	}
	if strings.TrimSpace(req.HostID) == "" {
		return Decision{}, fmt.Errorf("LPSM-BINDING-HOST: host id is required")
	}
	switch req.Mode {
	case ModeManaged, "":
		return Decision{
			Mode:        ModeManaged,
			EntryStyle:  "bridge",
			Reason:      "one-bridge default: served through the per-host LiteSPM bridge entry",
			NeedsBridge: true,
		}, nil
	case ModeNative:
		return Decision{
			Mode:        ModeNative,
			EntryStyle:  "native",
			Reason:      "explicit opt-in: binding.mode=native writes a per-package native entry",
			NeedsBridge: false,
		}, nil
	default:
		return Decision{}, fmt.Errorf("LPSM-BINDING-MODE: %q must be managed|native", req.Mode)
	}
}

// PluginComponent routes one child of a multi-component plugin bundle.
type PluginComponent struct {
	Name string               `json:"name"`
	Kind domain.ComponentKind `json:"kind"`
}

// SupportedComponent reports whether LiteSPM can materialise the component
// kind. "command" sources are rejected on import (ARCH/32 §5); connectors
// are declarative-only until D1 is re-decided.
func SupportedComponent(kind domain.ComponentKind) (bool, string) {
	switch kind {
	case domain.ComponentSkill,
		domain.ComponentMCPProvider,
		domain.ComponentHook,
		domain.ComponentAgent,
		domain.ComponentAgentDefinition,
		domain.ComponentRule,
		domain.ComponentTool,
		domain.ComponentLSP,
		domain.ComponentAsset:
		return true, "routed by component kind"
	case domain.ComponentCommand:
		return false, "command sources are rejected on import"
	default:
		return false, fmt.Sprintf("unknown component kind %q", kind)
	}
}

// RoutePlugin partitions a plugin's children into supported and rejected
// sets, preserving order. Rejections carry reasons; nothing is dropped
// silently.
func RoutePlugin(components []PluginComponent) (supported, rejected []PluginComponent) {
	for _, c := range components {
		if ok, _ := SupportedComponent(c.Kind); ok {
			supported = append(supported, c)
		} else {
			rejected = append(rejected, c)
		}
	}
	return supported, rejected
}
