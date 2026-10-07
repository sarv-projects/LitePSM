package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// DenyRulesFile is the file name, under the data root, that holds the user's
// explicit deny rules (a JSON array of DenyRule).
const DenyRulesFile = "policy-deny.json"

// LoadDenyRules reads the user deny tier from path. A missing file is the
// normal "no rules" state. A file that exists but cannot be parsed, or that
// contains a rule with no matching field or an unknown effect, is an error:
// silently dropping the user's deny tier would fail open.
func LoadDenyRules(path string) ([]DenyRule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read deny rules %s: %w", path, err)
	}
	var rules []DenyRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("parse deny rules %s: %w", path, err)
	}
	for i, r := range rules {
		if r.TargetRef == "" && r.Effect == "" && r.HostID == "" {
			return nil, fmt.Errorf("deny rule #%d (%q) in %s has no targetRef, effect or hostId", i, r.RuleID, path)
		}
		if r.Effect != "" && !ValidEffects[r.Effect] {
			return nil, fmt.Errorf("deny rule #%d (%q) in %s has unknown effect %q", i, r.RuleID, path, r.Effect)
		}
	}
	return rules, nil
}
