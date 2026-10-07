package porting

// conflict.go — conflict detection and the five strategies (ARCH/38 §5.3).
//
// A conflict is a name the target already has whose fingerprint differs from
// the source's. Identical fingerprints are not conflicts: the item is
// "unchanged" and nothing is written. Detection is by name, classification by
// fingerprint — never by eyeballing two configs.
//
// The default strategy `ask` leaves a conflict unresolved in the plan; the
// CLI resolves it from a terminal, or refuses (LPSM-COPY-004) when there is
// no terminal and no --yes. Every other strategy is applied while the plan is
// built, so the printed counts already show what `--conflict` will do.

import (
	"fmt"
	"strings"
)

// Conflict strategies.
const (
	StrategyAsk        = "ask"
	StrategyKeepTarget = "keep-target"
	StrategyUseSource  = "use-source"
	StrategyCompatible = "compatible"
	StrategySkip       = "skip"
)

// ConflictStrategies lists the valid --conflict values, in help order.
var ConflictStrategies = []string{
	StrategyAsk, StrategyKeepTarget, StrategyUseSource, StrategyCompatible, StrategySkip,
}

// ParseStrategy validates and canonicalizes a --conflict value. The empty
// string selects the default (ask).
func ParseStrategy(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return StrategyAsk, nil
	}
	for _, known := range ConflictStrategies {
		if v == known {
			return v, nil
		}
	}
	return "", fmt.Errorf("invalid --conflict %q: must be one of %s",
		s, strings.Join(ConflictStrategies, " | "))
}

// Conflict is one name both agents have with differing content.
type Conflict struct {
	Kind string
	ID   string
	// SourceFingerprint / TargetFingerprint are porting.Fingerprint values of
	// the two sides, so "why is this a conflict" is answerable from the plan
	// alone.
	SourceFingerprint string
	TargetFingerprint string
	// Detail is the one-line human explanation.
	Detail string
	// Resolution is the strategy decision already applied ("" = unresolved,
	// which only the `ask` strategy produces).
	Resolution string
}

// Key identifies a conflict inside a plan (kind and id together, because the
// same name may exist as both an MCP server and a skill).
func (c Conflict) Key() string { return ConflictKey(c.Kind, c.ID) }

// ConflictKey is the map key for one conflict.
func ConflictKey(kind, id string) string { return kind + "/" + id }

// ResolveConflict applies a strategy to one conflict and returns the item
// action plus the reason printed on the row.
//
// writeAction is what the item becomes when the source wins (ActionDirect or
// ActionTranslated): a resolved use-source copy writes exactly what a
// conflict-free copy would have written.
func ResolveConflict(strategy string, c Conflict, writeAction string) (action, reason string, err error) {
	switch strategy {
	case StrategyKeepTarget:
		return ActionSkipped, "conflict: the target entry is kept", nil
	case StrategySkip:
		return ActionSkipped, fmt.Sprintf("conflict: skipped (--conflict %s); the target is untouched", StrategySkip), nil
	case StrategyCompatible:
		// Both sides satisfy the target — the source passed the support check
		// to get here, and what sits on the target is by definition readable
		// there — so whichever copy already lives on the target is kept.
		// Neither an MCP entry nor a skill directory declares a version, so a
		// version difference is not knowable and is not claimed.
		return ActionSkipped, fmt.Sprintf(
			"conflict: both sides satisfy the target's format, so the existing target copy is kept; neither side declares a version to tell them apart (%s)",
			StrategyCompatible), nil
	case StrategyUseSource:
		return writeAction, "conflict: the source entry replaces the target entry (backup + ledger recorded)", nil
	case StrategyAsk:
		return ActionConflict, fmt.Sprintf(
			"unresolved conflict: choose --conflict %s, or answer interactively on a terminal",
			strings.Join(ConflictStrategies[1:], "|")), nil
	default:
		return "", "", fmt.Errorf("unknown conflict strategy %q", strategy)
	}
}
