package porting

import (
	"strings"
	"testing"
)

func TestParseStrategy(t *testing.T) {
	for _, in := range []string{"", "ask", " keep-target ", "USE-SOURCE", "compatible", "skip"} {
		got, err := ParseStrategy(in)
		if err != nil {
			t.Errorf("ParseStrategy(%q): %v", in, err)
			continue
		}
		want := strings.ToLower(strings.TrimSpace(in))
		if want == "" {
			want = StrategyAsk
		}
		if got != want {
			t.Errorf("ParseStrategy(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"force", "overwrite", "yes", "keep_target"} {
		if _, err := ParseStrategy(bad); err == nil {
			t.Errorf("ParseStrategy(%q) was accepted", bad)
		}
	}
}

// TestResolveConflictStrategies is the five-strategy contract: keep/skip/
// compatible leave the target alone, use-source rewrites the row back to the
// write action, and ask leaves it unresolved with the choice spelled out.
func TestResolveConflictStrategies(t *testing.T) {
	c := Conflict{Kind: KindMCP, ID: "github", Detail: "target exists, sources differ"}

	cases := []struct {
		strategy     string
		wantAction   string
		wantInReason string
	}{
		{StrategyKeepTarget, ActionSkipped, "kept"},
		{StrategySkip, ActionSkipped, "--conflict skip"},
		{StrategyCompatible, ActionSkipped, "version"},
		{StrategyUseSource, ActionTranslated, "replaces"},
		{StrategyAsk, ActionConflict, "unresolved"},
	}
	for _, tc := range cases {
		action, reason, err := ResolveConflict(tc.strategy, c, ActionTranslated)
		if err != nil {
			t.Errorf("%s: %v", tc.strategy, err)
			continue
		}
		if action != tc.wantAction {
			t.Errorf("%s: action = %q, want %q", tc.strategy, action, tc.wantAction)
		}
		if !strings.Contains(reason, tc.wantInReason) {
			t.Errorf("%s: reason %q must mention %q", tc.strategy, reason, tc.wantInReason)
		}
	}

	if _, _, err := ResolveConflict("force", c, ActionDirect); err == nil {
		t.Error("an unknown strategy was resolved instead of refused")
	}
}

func TestConflictKeyRoundTrip(t *testing.T) {
	c := Conflict{Kind: KindSkill, ID: "demo"}
	if c.Key() != "skill/demo" || ConflictKey("skill", "demo") != c.Key() {
		t.Errorf("key mismatch: %q vs %q", c.Key(), ConflictKey("skill", "demo"))
	}
}
