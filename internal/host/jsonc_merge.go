package host

// jsonc_merge.go — surgical JSON/JSONC member upsert.
//
// Why not just parse and re-marshal: several verified hosts keep user-authored
// `//` and `/* */` comments in their config (Zed, Amp, Qoder, CodeBuddy,
// CodeArts, Pochi). Parsing into map[string]any and calling MarshalIndent would
// silently delete every one of those comments — a data-loss bug in an installer
// whose whole job is to modify other people's config files safely.
//
// So for comment-tolerant hosts we locate the target object by byte offset and
// splice only the `litespm` member into it. Everything else in the file —
// comments, key order, indentation, blank lines — survives untouched.
//
// stripJSONComments replaces comments with equivalent whitespace, so offsets
// found in the stripped text are valid offsets into the original. That property
// is what makes this approach safe, and jsonc_offsets_preserved_strip_comments
// in the test file pins it.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// isJSONSpace reports whether b is JSON insignificant whitespace.
func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// trimJSONSpaceEdges narrows [start,end) past surrounding whitespace.
func trimJSONSpaceEdges(text string, start, end int) (int, int) {
	for start < end && isJSONSpace(text[start]) {
		start++
	}
	for end > start && isJSONSpace(text[end-1]) {
		end--
	}
	return start, end
}

// jsonValueSpan returns the byte range of the JSON value stored at keyPath,
// walking from the root object. found is false when the path does not exist.
// text must be comment-stripped (offsets are meaningful) and root must be a
// JSON object.
func jsonValueSpan(text string, keyPath []string) (start, end int, found bool, err error) {
	if len(keyPath) == 0 {
		return 0, 0, false, fmt.Errorf("empty key path")
	}
	dec := json.NewDecoder(strings.NewReader(text))
	span, found, err := descendForSpan(dec, text, keyPath, 0)
	if err != nil {
		return 0, 0, false, err
	}
	return span[0], span[1], found, nil
}

// descendForSpan recurses one key level down, returning the value span.
func descendForSpan(dec *json.Decoder, text string, keyPath []string, depth int) ([2]int, bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return [2]int{}, false, fmt.Errorf("expected object at depth %d: %w", depth, err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return [2]int{}, false, fmt.Errorf("expected object at depth %d, got %v", depth, tok)
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return [2]int{}, false, err
		}
		key, _ := keyTok.(string)
		if key != keyPath[depth] {
			if err := skipOneValue(dec); err != nil {
				return [2]int{}, false, err
			}
			continue
		}
		if depth == len(keyPath)-1 {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return [2]int{}, false, err
			}
			// InputOffset() reports the end of the last token, but the colon
			// separating key from value has already been consumed, so the
			// offset alone points one byte too far right. Deriving the start
			// from the decoded length is exact: RawMessage receives the value
			// bytes verbatim, without surrounding whitespace.
			end := int(dec.InputOffset())
			s, e := trimJSONSpaceEdges(text, end-len(raw), end)
			return [2]int{s, e}, true, nil
		}
		return descendForSpan(dec, text, keyPath, depth+1)
	}
	return [2]int{}, false, nil
}

// skipOneValue advances the decoder past exactly one value.
func skipOneValue(dec *json.Decoder) error {
	var raw json.RawMessage
	return dec.Decode(&raw)
}

// lineIndentOf returns the whitespace between the start of the line containing
// offset and that offset. It returns "" when the line holds other content
// before the offset, so we never invent indentation mid-expression.
func lineIndentOf(text string, offset int) string {
	lineStart := strings.LastIndex(text[:offset], "\n") + 1
	candidate := text[lineStart:offset]
	if strings.TrimSpace(candidate) != "" {
		return ""
	}
	return candidate
}

// indentValue renders a compact JSON value with continuation lines indented to
// match the surrounding document.
func indentValue(value any, indent string) (string, error) {
	compact, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(compact), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n"), nil
}

// upsertObjectMember inserts or replaces one member of a JSON object literal,
// leaving the rest of the literal byte-identical. objText may contain comments;
// they are stripped for locating only, never rewritten.
func upsertObjectMember(objText, name string, value any) (string, error) {
	locatable := stripJSONComments(objText)
	start, end, found, err := jsonValueSpan(locatable, []string{name})
	if err != nil && strings.Contains(err.Error(), "expected object") {
		return objText, nil // not an object literal; leave it for the caller
	}
	if err != nil {
		return "", err
	}
	if found {
		rendered, err := indentValue(value, lineIndentOf(objText, start))
		if err != nil {
			return "", err
		}
		return objText[:start] + rendered + objText[end:], nil
	}
	closeRel := strings.LastIndex(objText, "}")
	if closeRel < 0 {
		return "", fmt.Errorf("object literal has no closing brace")
	}
	rendered, err := indentValue(value, lineIndentOf(objText, closeRel)+"  ")
	if err != nil {
		return "", err
	}
	return insertRawMember(objText, closeRel, name, rendered)
}

// insertRawMember writes `"name": literal` into an object literal immediately
// before its closing brace at offset closeRel.
func insertRawMember(objText string, closeRel int, name, literal string) (string, error) {
	closeIndent := lineIndentOf(objText, closeRel)
	memberIndent := closeIndent + "  "
	body := strings.TrimRight(objText[:closeRel], " \t\r\n")
	member := fmt.Sprintf("%q: %s", name, literal)
	if isEmptyJSONObject(body) {
		return body + "\n" + memberIndent + member + "\n" + closeIndent + objText[closeRel:], nil
	}
	return body + ",\n" + memberIndent + member + "\n" + closeIndent + objText[closeRel:], nil
}

// isEmptyJSONObject reports whether a `{...}` prefix contains no members.
func isEmptyJSONObject(body string) bool {
	return strings.TrimSpace(body) == "{"
}

// chainLiteral renders the value for a container member whose NAME is supplied
// by the caller, containing the remaining container chain in keys and, at the
// leaf, the `litespm` member holding entry.
//
//	keys = []          -> {"litespm": entry}             (member "mcpServers")
//	keys = ["servers"] -> {"servers": {"litespm": ...}}  (member "mcp")
//
// keyPath names containers only and `litespm` is always the leaf member. The
// previous helper hardcoded `litespm` at the leaf and dropped intermediate
// names, so a two-level path such as opencode's `mcp.servers` produced a
// spurious extra `mcp` level. No data-driven target used a two-level path, so
// the defect stayed latent until the bespoke adapters were routed here.
func chainLiteral(keys []string, entry string, indent string) string {
	inner := indent + "  "
	if len(keys) == 0 {
		return "{\n" + inner + fmt.Sprintf("%q: %s", litespmServerName, entry) + "\n" + indent + "}"
	}
	child := chainLiteral(keys[1:], entry, inner)
	return "{\n" + inner + fmt.Sprintf("%q: %s", keys[0], child) + "\n" + indent + "}"
}

// ensureKeyPath makes sure the object at keyPath exists, creating any missing
// intermediate levels, and returns the resulting text plus the byte range of
// that object's value.
func ensureKeyPath(raw string, keyPath []string, entry any) (string, int, int, error) {
	if len(keyPath) == 0 {
		return "", 0, 0, fmt.Errorf("empty key path")
	}
	entryCompact, err := json.Marshal(entry)
	if err != nil {
		return "", 0, 0, err
	}
	cur := raw
	for depth := range keyPath {
		locatable := stripJSONComments(cur)
		s, e, found, err := jsonValueSpan(locatable, keyPath[:depth+1])
		if err != nil {
			return "", 0, 0, err
		}
		if found {
			if !strings.HasPrefix(strings.TrimSpace(locatable[s:e]), "{") {
				return "", 0, 0, fmt.Errorf(
					"key %q holds a non-object value; refusing to overwrite it", keyPath[depth])
			}
			continue
		}
		containerStart, containerEnd := 0, len(cur)
		if depth > 0 {
			s, e, found, err := jsonValueSpan(locatable, keyPath[:depth])
			if err != nil {
				return "", 0, 0, err
			}
			if !found {
				return "", 0, 0, fmt.Errorf("intermediate key %v missing unexpectedly", keyPath[:depth])
			}
			containerStart, containerEnd = s, e
		}
		objText := cur[containerStart:containerEnd]
		memberIndent := lineIndentOf(cur, containerStart) + "  "
		// The inserted member is named keyPath[depth]; its value is the rest of
		// the container chain plus the `litespm` leaf.
		value := chainLiteral(keyPath[depth+1:], string(entryCompact), memberIndent)
		closeRel := strings.LastIndex(objText, "}")
		if closeRel < 0 {
			return "", 0, 0, fmt.Errorf("cannot create %v: parent is not an object", keyPath[:depth])
		}
		updated, err := insertRawMember(objText, closeRel, keyPath[depth], value)
		if err != nil {
			return "", 0, 0, err
		}
		cur = cur[:containerStart] + updated + cur[containerEnd:]
	}
	locatable := stripJSONComments(cur)
	s, e, found, err := jsonValueSpan(locatable, keyPath)
	if err != nil {
		return "", 0, 0, err
	}
	if !found {
		return "", 0, 0, fmt.Errorf("failed to create key path %v", keyPath)
	}
	return cur, s, e, nil
}

// mergeJSONEntrySurgical upserts the `litespm` member into the object at
// keyPath without reformatting the rest of the document. Comments, key order
// and indentation outside the touched object are preserved byte-for-byte.
func mergeJSONEntrySurgical(raw string, keyPath []string, value any) (string, error) {
	return mergeJSONEntrySurgicalNamed(raw, keyPath, litespmServerName, value)
}

// mergeJSONEntrySurgicalNamed is mergeJSONEntrySurgical for an arbitrary member
// name. The bridge is always called `litespm`, but a catalog install writes the
// SERVER's name into the same container, so the name cannot be hardcoded here.
func mergeJSONEntrySurgicalNamed(raw string, keyPath []string, name string, value any) (string, error) {
	if strings.TrimSpace(stripJSONComments(raw)) == "" {
		raw = "{}"
	}
	cur, start, end, err := ensureKeyPath(raw, keyPath, value)
	if err != nil {
		return "", err
	}
	objText := cur[start:end]
	updated, err := upsertObjectMember(objText, name, value)
	if err != nil {
		return "", err
	}
	if updated == objText {
		return cur, nil
	}
	return cur[:start] + updated + cur[end:], nil
}
