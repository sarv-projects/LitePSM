package porting

import (
	"reflect"
	"testing"
)

func TestClassifyEnvSplitsReferencesFromLiterals(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN": "ghp_literal_value", // a hand-typed credential
		"X_API":        "${X_API}",          // shell-style reference, key == name
		"CURSOR_KEY":   "${env:CURSOR_KEY}", // explicit-namespace reference
		"OC_KEY":       "{env:OC_KEY}",      // OpenCode's reference spelling
		"FALLBACK":     "${FALLBACK:-none}", // reference with a recorded fallback
		"TOKEN":        "${SECRET}",         // key and reference name differ: inexpressible
		"MIXED":        "prefix-${MIXED}",   // interpolated: not a form the package emits
	}
	got := ClassifyEnv(env, nil)

	wantRefs := []string{"CURSOR_KEY", "FALLBACK", "OC_KEY", "X_API"}
	if !reflect.DeepEqual(got.Refs, wantRefs) {
		t.Errorf("Refs = %v, want %v", got.Refs, wantRefs)
	}
	wantDropped := []string{"GITHUB_TOKEN", "MIXED", "TOKEN"}
	if !reflect.DeepEqual(got.Dropped, wantDropped) {
		t.Errorf("Dropped = %v, want %v", got.Dropped, wantDropped)
	}
}

func TestClassifyEnvNameListIsAReferenceEvenWithAnEmptyValue(t *testing.T) {
	// Codex-style content: names in a list, empty placeholder values in the
	// map the read path builds. The name must not be demoted to a literal.
	env := map[string]string{"A": ""}
	got := ClassifyEnv(env, []string{"A", "B"})
	if !reflect.DeepEqual(got.Refs, []string{"A", "B"}) {
		t.Errorf("Refs = %v, want [A B]", got.Refs)
	}
	if len(got.Dropped) != 0 {
		t.Errorf("Dropped = %v, want empty", got.Dropped)
	}
}

func TestNeedsUnionsReferencesAndDroppedNames(t *testing.T) {
	env := map[string]string{"LITERAL": "hunter2", "REF": "${REF}"}
	got := ClassifyEnv(env, nil)
	want := []string{"LITERAL", "REF"}
	if !reflect.DeepEqual(got.Needs(), want) {
		t.Errorf("Needs() = %v, want %v", got.Needs(), want)
	}
}

func TestReferenceNameRejectsNonReferenceValues(t *testing.T) {
	for _, v := range []string{
		"",
		"hunter2",
		"${UNCLOSED",
		"${}",
		"${not a name}",
		"{env:}",
		"a${b}c", // interpolated text is passed through untouched, never expanded
	} {
		if name, ok := referenceName(v); ok {
			t.Errorf("referenceName(%q) = %q, true; want false", v, name)
		}
	}
	if name, ok := referenceName("  ${API_KEY}  "); !ok || name != "API_KEY" {
		t.Errorf("referenceName(quoted ${API_KEY}) = %q, %v; want API_KEY, true", name, ok)
	}
}
