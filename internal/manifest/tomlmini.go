// Minimal TOML-subset parser shared by the manifest, lockfile and profile
// readers. It supports exactly what the canonical writers emit plus
// hand-authored equivalents: comments, [table], [[array-table]], and scalar
// / inline-array values. Anything else is rejected rather than guessed.
package manifest

import (
	"fmt"
	"strconv"
	"strings"
)

type tomlDoc struct {
	scalars     map[string]string
	tables      map[string]map[string]string
	arrayTables map[string][]map[string]string
}

func parseTOMLSubset(data []byte) (*tomlDoc, error) {
	doc := &tomlDoc{
		scalars:     map[string]string{},
		tables:      map[string]map[string]string{},
		arrayTables: map[string][]map[string]string{},
	}
	var curTable string
	var curArrayTable string
	curArrayIdx := -1

	set := func(key, val string) error {
		if curArrayTable != "" {
			tbls := doc.arrayTables[curArrayTable]
			if curArrayIdx < 0 || curArrayIdx >= len(tbls) {
				return fmt.Errorf("internal parser state: no current [[%s]] entry", curArrayTable)
			}
			tbls[curArrayIdx][key] = val
			return nil
		}
		if curTable != "" {
			if _, ok := doc.tables[curTable]; !ok {
				doc.tables[curTable] = map[string]string{}
			}
			doc.tables[curTable][key] = val
			return nil
		}
		doc.scalars[key] = val
		return nil
	}

	for ln, line := range strings.Split(string(data), "\n") {
		// Strip comments (a # inside quotes is preserved).
		line = stripComment(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[[") {
			if !strings.HasSuffix(line, "]]") {
				return nil, fmt.Errorf("line %d: unclosed [[table]]", ln+1)
			}
			name := strings.TrimSpace(line[2 : len(line)-2])
			if name == "" || strings.ContainsAny(name, " \t\"'") {
				return nil, fmt.Errorf("line %d: bad array table name %q", ln+1, name)
			}
			doc.arrayTables[name] = append(doc.arrayTables[name], map[string]string{})
			curArrayTable = name
			curTable = ""
			curArrayIdx = len(doc.arrayTables[name]) - 1
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: unclosed [table]", ln+1)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" {
				return nil, fmt.Errorf("line %d: empty table name", ln+1)
			}
			curTable = name
			curArrayTable = ""
			curArrayIdx = -1
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", ln+1)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" || val == "" {
			return nil, fmt.Errorf("line %d: bad key/value pair", ln+1)
		}
		if err := set(key, val); err != nil {
			return nil, fmt.Errorf("line %d: %w", ln+1, err)
		}
	}
	return doc, nil
}

func stripComment(line string) string {
	inStr := false
	esc := false
	for i, r := range line {
		switch {
		case esc:
			esc = false
		case r == '\\' && inStr:
			esc = true
		case r == '"':
			inStr = !inStr
		case r == '#' && !inStr:
			return line[:i]
		}
	}
	return line
}

func unquote(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		u, err := strconv.Unquote(s)
		if err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		return s[1 : len(s)-1]
	}
	return s
}

func quote(s string) string { return strconv.Quote(s) }

func parseStrArray(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		if s == "" {
			return nil
		}
		return []string{unquote(s)}
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return nil
	}
	var out []string
	for _, part := range splitCSV(inner) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, unquote(part))
	}
	return out
}

func splitCSV(s string) []string {
	var parts []string
	var cur strings.Builder
	inStr := false
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && inStr:
			cur.WriteRune(r)
			esc = true
		case r == '"':
			inStr = !inStr
			cur.WriteRune(r)
		case r == ',' && !inStr:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	parts = append(parts, cur.String())
	return parts
}

func quoteStrArray(items []string) string {
	qs := make([]string, 0, len(items))
	for _, it := range items {
		qs = append(qs, quote(it))
	}
	return "[" + strings.Join(qs, ", ") + "]"
}

func parseInt(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}
