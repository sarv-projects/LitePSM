package host

// toml_verify.go — comment-aware TOML registration checks.
//
// VerifySetup for the TOML hosts used to answer "is the bridge registered?"
// with
//
//	strings.Contains(content, "[mcp_servers.litespm]") && strings.Contains(content, "bridge")
//
// Both halves match commented text: a hand-disabled table
// (`#[mcp_servers.litespm]`) contains the header as a substring, and the word
// "bridge" is satisfied by any comment or unrelated section. A host whose
// bridge the user had commented out therefore reported ready and no setup was
// offered — the exact self-confirming failure the JSON adapters already avoid
// by parsing their config instead of searching it.
//
// These helpers parse TOML the way parseTomlMcpComponents and mergeTOMLEntry
// already do: line-wise, dropping `#` comments (including trailing ones)
// before a header or value is matched. Like every line-wise reader here they
// do not model multi-line basic/literal strings; a `#` inside one is treated
// as a comment start, which cannot produce a false "ready" — it can only make
// a match fail, and that failure is reported as "missing" (setup is offered
// again), never as registered.

import "strings"

// stripTOMLComment returns line with any trailing `#` comment removed and the
// surrounding whitespace trimmed. Quote characters are honoured, so
// `command = "app#1"` keeps its value intact.
func stripTOMLComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			switch {
			case quote == '"' && c == '\\':
				i++ // skip the escaped byte
			case c == quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '#':
			return strings.TrimSpace(line[:i])
		}
	}
	return strings.TrimSpace(line)
}

// tomlSectionLines returns the live lines that belong to table (a header
// written exactly as e.g. "[mcp_servers.litespm]") and reports whether such a
// live header exists at all. Comment-only lines contribute nothing, and a
// commented header opens no section, so `#[mcp_servers.litespm]` is neither a
// section nor a registration.
func tomlSectionLines(content, table string) ([]string, bool) {
	var section []string
	inSection := false
	found := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := stripTOMLComment(line)
		if trimmed == "" {
			continue
		}
		if trimmed[0] == '[' {
			inSection = trimmed == table
			if inSection {
				found = true
			}
			continue
		}
		if inSection {
			section = append(section, trimmed)
		}
	}
	return section, found
}

// tomlTableRegistered reports whether table exists as a live (non-commented)
// TOML table header in content.
func tomlTableRegistered(content, table string) bool {
	_, found := tomlSectionLines(content, table)
	return found
}

// tomlBridgeRegistered reports whether table exists as a live header and some
// live line inside it mentions "bridge" — the argv every LiteSPM bridge entry
// carries (`args = ["bridge", "stdio", "--host", ...]`). Scoping the second
// requirement to the table means a "bridge" word in an unrelated section or
// comment no longer registers the host as configured.
func tomlBridgeRegistered(content, table string) bool {
	section, found := tomlSectionLines(content, table)
	if !found {
		return false
	}
	for _, line := range section {
		if strings.Contains(line, "bridge") {
			return true
		}
	}
	return false
}
