package host

// remove.go — reversing an install.
//
// Everything LiteSPM writes into someone else's configuration must be
// removable by the same tool. Before this file there was no removal path at
// all: `litespm` could inject a bridge entry into 50 different agent configs
// and offered no way to take it back out. That is unacceptable for a tool whose
// entire job is editing files it does not own.
//
// Removal is the inverse of merge, and it is held to the same standard:
//
//   - the file is backed up before it is touched;
//   - only the `litespm` entry is removed, never a sibling;
//   - comments, key order and formatting elsewhere survive byte-for-byte;
//   - if the entry is not present, the file is left alone and the caller is
//     told so rather than being handed a no-op that looks like success;
//   - the result is re-parsed and asserted to be valid before anything is
//     written.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// removalSpec describes how to find the litespm entry in one host's config.
type removalSpec struct {
	Format  ConfigFormat
	KeyPath []string
	// Candidates are alternative container paths, tried in order. OpenCode has
	// three documented shapes (v1 flat `mcp.<name>`, v2 nested `mcp.servers`,
	// and the `mcpServers` spelling some configs use), and the layout a file
	// actually uses can differ from the one the adapter last wrote — for
	// example after an upgrade, or when a user reorganised their config by
	// hand. Removal must find the entry wherever it is, or `host remove`
	// silently leaves a bridge registration behind.
	Candidates [][]string
}

// effectiveKeyPaths returns the paths to try, most likely first.
func (s removalSpec) effectiveKeyPaths() [][]string {
	if len(s.Candidates) > 0 {
		return s.Candidates
	}
	if len(s.KeyPath) == 0 {
		return nil
	}
	return [][]string{s.KeyPath}
}

// bespokeRemovalSpecs covers the hand-written adapters, which do not expose a
// BridgeTarget. These key paths mirror what each adapter writes.
var bespokeRemovalSpecs = map[string]removalSpec{
	"claude-code": {Format: FormatJSON, KeyPath: []string{"mcpServers"}},
	"cline":       {Format: FormatJSON, KeyPath: []string{"mcpServers"}},
	"pi-agent":    {Format: FormatJSON, KeyPath: []string{"mcpServers"}},
	"pi":          {Format: FormatJSON, KeyPath: []string{"mcpServers"}},
	"codex":       {Format: FormatTOML, KeyPath: []string{"mcp_servers"}},
	"grok":        {Format: FormatTOML, KeyPath: []string{"mcp_servers"}},
	"grok-build":  {Format: FormatTOML, KeyPath: []string{"mcp_servers"}},
}

// removalSpecFor resolves the removal spec for any registered adapter.
//
// OpenCode is the one host with two documented layouts (v1 `mcpServers`, v2
// `mcp.servers`), so its spec is chosen by reading the file rather than assumed.
func removalSpecFor(adapter HostAdapter) (removalSpec, error) {
	id := strings.ToLower(adapter.Descriptor().HostID)

	if g, ok := adapter.(*GenericAdapter); ok {
		userKey := g.Target.keyPathFor(false)
		projectKey := g.Target.keyPathFor(true)
		spec := removalSpec{
			Format:  g.Target.Format,
			KeyPath: userKey,
		}
		// Hosts whose user and project containers differ (fx: `mcp` in the
		// user file, `mcpServers` in a project `.mcp.json`) may hold the
		// bridge entry under either key, depending on which scope wrote it.
		// Trying only the user key left a project-scope registration behind
		// while removal reported success.
		if strings.Join(userKey, ".") != strings.Join(projectKey, ".") && len(projectKey) > 0 {
			spec.Candidates = [][]string{userKey, projectKey}
		}
		return spec, nil
	}

	if id == "opencode" {
		return openCodeRemovalSpec(), nil
	}
	if spec, ok := bespokeRemovalSpecs[id]; ok {
		return spec, nil
	}
	return removalSpec{}, fmt.Errorf("no removal path is defined for host %q", id)
}

// openCodeRemovalSpec lists every documented OpenCode container shape.
//
// OpenCode is the one host with more than one layout: v1 keeps servers
// directly under `mcp`, v2 nests them under `mcp.servers`, and some configs
// use the `mcpServers` spelling. Which shape a file uses can also differ from
// the one the adapter last wrote — after an upgrade, or when a user
// reorganised their config by hand — so removal tries each in turn and
// rewrites only the one that actually holds an entry. Deriving the path by
// reading the file is not enough on its own: a config that fails to parse
// (JSONC, a stray trailing comma) would silently push removal onto the wrong
// path and leave the bridge registration behind.
func openCodeRemovalSpec() removalSpec {
	paths := [][]string{{"mcp", "servers"}, {"mcp"}, {"mcpServers"}}
	return removalSpec{Format: FormatJSON, KeyPath: paths[0], Candidates: paths}
}

// RemovalResult reports exactly what happened, so a caller can distinguish
// "removed", "was not there" and "could not".
type RemovalResult struct {
	HostID     string
	ConfigPath string
	BackupPath string
	Removed    bool
	Reason     string
}

// PlanRemoval produces the content that would result from removing the bridge
// entry, without writing anything.
func PlanRemoval(ctx context.Context, adapter HostAdapter, backupDir string) (*HostChangePlan, *RemovalResult, error) {
	configPath, err := adapter.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &RemovalResult{
				HostID:     adapter.Descriptor().HostID,
				ConfigPath: configPath,
				Removed:    false,
				Reason:     "no config file at this path",
			}, nil
		}
		return nil, nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	orig := string(data)

	spec, err := removalSpecFor(adapter)
	if err != nil {
		return nil, nil, err
	}

	// Try every documented container path: the first one that actually holds a
	// bridge entry is the one the adapter wrote, and it is the only one we
	// rewrite. A file that matches none of them is reported as "not present"
	// rather than rewritten blindly. A file that cannot be parsed at all is an
	// error, not a quiet not-present: every candidate fails the same way, and
	// the first parse failure is surfaced so a corrupt config is never
	// misreported as clean.
	proposed, removed := orig, false
	effective := spec.KeyPath
	var candidateErr error
	for _, candidate := range spec.effectiveKeyPaths() {
		attempt := spec
		attempt.KeyPath = candidate
		updated, didRemove, err := stripBridgeEntry(orig, attempt)
		if err != nil {
			if candidateErr == nil {
				candidateErr = err
			}
			continue
		}
		if didRemove {
			proposed, removed, effective = updated, true, candidate
			candidateErr = nil
			break
		}
	}
	if candidateErr != nil {
		return nil, nil, fmt.Errorf("%s: %w", adapter.Descriptor().DisplayName, candidateErr)
	}
	if !removed {
		return nil, &RemovalResult{
			HostID:     adapter.Descriptor().HostID,
			ConfigPath: configPath,
			Removed:    false,
			Reason:     "the litespm entry is not present in this file",
		}, nil
	}
	spec.KeyPath = effective

	// Never write a config we cannot read back.
	if spec.Format == FormatJSON {
		verify, err := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(proposed))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: removal would produce invalid JSON, refusing to write: %w",
				adapter.Descriptor().DisplayName, err)
		}
		if inspectNamedEntry(verify, spec.KeyPath, litespmServerName) || inspectNamedEntry(verify, spec.KeyPath, legacyServerName) {
			return nil, nil, fmt.Errorf("%s: removal did not take effect, refusing to write",
				adapter.Descriptor().DisplayName)
		}
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, adapter.Descriptor().HostID)
	if err != nil {
		return nil, nil, err
	}

	return &HostChangePlan{
			HostID:          adapter.Descriptor().HostID,
			ConfigPath:      configPath,
			OriginalContent: orig,
			ProposedContent: proposed,
			BackupPath:      backupPath,
		}, &RemovalResult{
			HostID:     adapter.Descriptor().HostID,
			ConfigPath: configPath,
			BackupPath: backupPath,
			Removed:    true,
		}, nil
}

// stripBridgeEntry removes every LiteSPM bridge entry (the current server name
// and the pre-rename legacy name), reporting whether anything was present.
// Siblings and surrounding formatting are untouched.
func stripBridgeEntry(content string, spec removalSpec) (string, bool, error) {
	out := content
	removedAny := false
	for _, name := range []string{litespmServerName, legacyServerName} {
		updated, removed, err := stripServerEntry(out, spec, name)
		if err != nil {
			return "", false, err
		}
		out = updated
		removedAny = removedAny || removed
	}
	return out, removedAny, nil
}

// stripServerEntry removes one named server entry in the format of spec.
func stripServerEntry(content string, spec removalSpec, serverName string) (string, bool, error) {
	if spec.Format == FormatTOML {
		return stripTOMLEntryNamed(content, spec.KeyPath, serverName)
	}
	return stripJSONEntryNamed(content, spec.KeyPath, serverName)
}

// stripJSONEntry deletes `litespm` from the object at keyPath.
func stripJSONEntry(content string, keyPath []string) (string, bool, error) {
	return stripJSONEntryNamed(content, keyPath, litespmServerName)
}

// stripJSONEntryNamed deletes the member named serverName from the object at
// keyPath.
func stripJSONEntryNamed(content string, keyPath []string, serverName string) (string, bool, error) {
	if strings.TrimSpace(content) == "" {
		return content, false, nil
	}
	locatable := stripJSONComments(content)

	objStart, objEnd, found, err := jsonValueSpan(locatable, keyPath)
	if err != nil {
		// A malformed config is not "entry absent": reporting not-removed
		// here silently left the bridge registration behind while the caller
		// believed the file was clean. Surface the parse failure so the
		// caller fails closed instead of misreporting.
		return "", false, fmt.Errorf("parsing config to remove %q: %w", serverName, err)
	}
	if !found {
		return content, false, nil
	}

	objText := content[objStart:objEnd]
	valueStart, valueEnd, present, err := jsonValueSpan(stripJSONComments(objText), []string{serverName})
	if err != nil {
		return "", false, fmt.Errorf("parsing config to remove %q: %w", serverName, err)
	}
	if !present {
		return content, false, nil
	}

	// jsonValueSpan locates the VALUE. A member is `"key": value`, so the key
	// and the colon have to be included or the result is `{"litespm":}`.
	memberStart := keyStartForValue(objText, valueStart)

	updated, err := deleteObjectMember(objText, memberStart, valueEnd)
	if err != nil {
		return "", false, err
	}

	merged := content[:objStart] + updated + content[objEnd:]
	cutPos := objStart + memberStart

	// A comment line immediately above the removed member belongs to it.
	// Leaving it behind strands the member before the cut as a dangling comma
	// followed by a comment, which is invalid JSON — so the removal would be
	// refused and the bridge entry could never be removed from a JSONC config.
	// Drop such comments (bounded) until the document parses again. Files
	// without this shape are untouched: the first parse check passes.
	for attempt := 0; attempt < 6; attempt++ {
		if _, perr := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(merged)); perr == nil {
			break
		}
		// Syntactic repairs first: they are position-independent. A comma left
		// before a closing brace is the common residue of removing a member, and
		// clearing it usually restores validity on its own.
		if repaired, fixed := dropDanglingCommaBeforeBrace(merged); fixed {
			merged = repaired
			continue
		}
		if next, moved, ok := dropPrecedingCommentLine(merged, cutPos); ok {
			merged, cutPos = next, moved
			continue
		}
		break
	}
	return merged, true, nil
}

// dropDanglingCommaBeforeBrace removes a `,` left immediately before a closing
// brace. Removing the last member of an object leaves exactly that, and JSON
// forbids it. It runs only when the document no longer parses, so a well-formed
// removal is never touched.
func dropDanglingCommaBeforeBrace(text string) (string, bool) {
	for i := 0; i < len(text); i++ {
		if text[i] != '}' {
			continue
		}
		j := i - 1
		for j >= 0 {
			if isJSONSpace(text[j]) {
				j--
				continue
			}
			// A comment line between the comma and the brace hides the comma;
			// step over it the same way whitespace is stepped over.
			if before, ok := startOfCommentLineBefore(text, j); ok {
				j = before
				continue
			}
			break
		}
		if j >= 0 && text[j] == ',' {
			return text[:j] + text[j+1:], true
		}
	}
	return text, false
}

// startOfCommentLineBefore reports whether the line ending at j is a comment
// line, and if so returns the offset just before that line so a backward scan
// can step over the whole comment.
func startOfCommentLineBefore(text string, j int) (int, bool) {
	if j < 0 || j >= len(text) {
		return j, false
	}
	lineStart := strings.LastIndex(text[:j+1], "\n") + 1
	trimmed := strings.TrimSpace(text[lineStart : j+1])
	if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") {
		return j, false
	}
	return lineStart - 1, true
}

// dropPrecedingCommentLine removes a comment line that belongs to the member
// cut at pos, together with the blank/indented lines between them, returning
// the shortened text and the adjusted position. It walks back over
// whitespace-only lines because the cut usually starts at an indented quote.
func dropPrecedingCommentLine(text string, pos int) (string, int, bool) {
	if pos <= 0 || pos > len(text) {
		return text, pos, false
	}
	search := pos
	for i := 0; i < 6; i++ {
		if search < 0 {
			break
		}
		lineStart := strings.LastIndex(text[:search], "\n") + 1
		trimmed := strings.TrimSpace(text[lineStart:search])
		if trimmed == "" {
			search = lineStart - 1
			continue
		}
		if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") {
			break
		}
		end := lineStart
		if end > 0 {
			end-- // take the newline that opened the comment line with it
		}
		return text[:end] + text[pos:], end, true
	}
	return text, pos, false
}

// keyStartForValue walks backwards from a value to the opening quote of the key
// that names it, so a member can be removed whole rather than leaving a
// dangling `"key":`.
func keyStartForValue(text string, valueStart int) int {
	i := valueStart - 1
	for i >= 0 && isJSONSpace(text[i]) {
		i--
	}
	if i >= 0 && text[i] == ':' {
		i--
	}
	for i >= 0 && isJSONSpace(text[i]) {
		i--
	}
	if i < 0 || text[i] != '"' {
		return valueStart
	}
	// Scan back to the unescaped opening quote.
	for j := i - 1; j >= 0; j-- {
		if text[j] != '"' {
			continue
		}
		backslashes := 0
		for k := j - 1; k >= 0 && text[k] == '\\'; k-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return j
		}
	}
	return valueStart
}

// deleteObjectMember removes the member spanning [memberStart, memberEnd),
// taking exactly one adjacent comma with it so the result stays valid JSON.
func deleteObjectMember(text string, memberStart, memberEnd int) (string, error) {
	if memberStart < 0 || memberEnd > len(text) || memberStart >= memberEnd {
		return "", fmt.Errorf("invalid member span %d..%d", memberStart, memberEnd)
	}

	// Preferred case: the member occupies its own line, which is how every
	// config we write is formatted. Removing the whole line keeps the file tidy.
	lineStart := strings.LastIndex(text[:memberStart], "\n") + 1
	if strings.TrimSpace(text[lineStart:memberStart]) == "" {
		rest := text[memberEnd:]
		nl := strings.Index(rest, "\n")
		if nl < 0 {
			nl = len(rest)
		}
		trailing := strings.TrimSpace(text[memberEnd : memberEnd+nl])
		if trailing == "" || trailing == "," {
			after := memberEnd + nl
			if after < len(text) {
				after++ // consume the newline itself
			}
			out := text[:lineStart] + text[after:]
			// If the removed member carried no trailing comma it was the LAST
			// member, so the comma before it now dangles and must go. If it did
			// carry one, that comma already separated it from the next member
			// and the preceding comma is still doing its job.
			if trailing == "" {
				j := lineStart
				for j > 0 && isJSONSpace(out[j-1]) {
					j--
				}
				if j > 0 && out[j-1] == ',' {
					out = out[:j-1] + out[j:]
				}
			}
			return out, nil
		}
	}

	// Same-line member: consume a following comma, else a preceding one.
	i := memberEnd
	for i < len(text) && isJSONSpace(text[i]) {
		i++
	}
	if i < len(text) && text[i] == ',' {
		i++
		for i < len(text) && isJSONSpace(text[i]) {
			i++
		}
		return text[:memberStart] + text[i:], nil
	}

	// No trailing comma, so this was the LAST member: take the comma that
	// separated it from the previous one. The scan has to look through a
	// comment line, because a member documented with `// ...` directly above
	// it puts the comma before the comment rather than immediately before the
	// member; without skipping it the comma dangles and the document stops
	// parsing.
	j := memberStart
	for j > 0 {
		c := text[j-1]
		if isJSONSpace(c) {
			j--
			continue
		}
		if c == ',' {
			j--
			for j > 0 && isJSONSpace(text[j-1]) {
				j--
			}
			break
		}
		if c == '/' && j >= 2 && text[j-2] == '/' {
			// Step back over this comment line and keep looking.
			for j > 0 && text[j-1] != '\n' {
				j--
			}
			continue
		}
		break
	}
	return text[:j] + text[memberEnd:], nil
}

// stripTOMLEntry removes one `[prefix.litespm]` table and its keys.
func stripTOMLEntry(content string, keyPath []string) (string, bool, error) {
	return stripTOMLEntryNamed(content, keyPath, litespmServerName)
}

// stripTOMLEntryNamed removes one `[prefix.<serverName>]` table and its keys.
func stripTOMLEntryNamed(content string, keyPath []string, serverName string) (string, bool, error) {
	table := "[" + strings.Join(append(append([]string{}, keyPath...), serverName), ".") + "]"
	lines := strings.Split(content, "\n")
	var out []string
	skipping := false
	removed := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == table {
			skipping = true
			removed = true
			continue
		}
		if skipping {
			if strings.HasPrefix(trimmed, "[") {
				skipping = false
			} else {
				continue // a key or blank line belonging to the removed table
			}
		}
		out = append(out, line)
	}
	if !removed {
		return content, false, nil
	}
	// Collapse any run of blank lines the removal left behind.
	result := strings.Join(out, "\n")
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimRight(result, "\n") + "\n", true, nil
}

// RemoveSetup removes the bridge entry from one host and reports what happened.
// It is the inverse of ApplySetup and is safe to call on a host that was never
// installed into: that case returns Removed=false with a reason.
func RemoveSetup(ctx context.Context, adapter HostAdapter, backupDir string) (*RemovalResult, error) {
	plan, result, err := PlanRemoval(ctx, adapter, backupDir)
	if err != nil || plan == nil {
		return result, err
	}
	if _, err := adapter.ApplySetup(ctx, plan); err != nil {
		return nil, fmt.Errorf("applying removal to %s: %w", plan.ConfigPath, err)
	}
	return result, nil
}

// RemoveFromAll removes the bridge entry from every host that currently has it,
// and reports per-host outcomes. It never fails the whole run because one host
// errored: a partial uninstall that reports honestly is more useful than an
// all-or-nothing one that leaves the user guessing.
func RemoveFromAll(ctx context.Context, backupDir string) []RemovalResult {
	var results []RemovalResult
	for _, adapter := range ListAdapters() {
		result, err := RemoveSetup(ctx, adapter, backupDir)
		if err != nil {
			results = append(results, RemovalResult{
				HostID: adapter.Descriptor().HostID,
				Reason: err.Error(),
			})
			continue
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	return results
}
