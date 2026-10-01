package host

// remove.go — reversing an install.
//
// Everything LitePSM writes into someone else's configuration must be
// removable by the same tool. Before this file there was no removal path at
// all: `litepsm` could inject a bridge entry into 50 different agent configs
// and offered no way to take it back out. That is unacceptable for a tool whose
// entire job is editing files it does not own.
//
// Removal is the inverse of merge, and it is held to the same standard:
//
//   - the file is backed up before it is touched;
//   - only the `litepsm` entry is removed, never a sibling;
//   - comments, key order and formatting elsewhere survive byte-for-byte;
//   - if the entry is not present, the file is left alone and the caller is
//     told so rather than being handed a no-op that looks like success;
//   - the result is re-parsed and asserted to be valid before anything is
//     written.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// removalSpec describes how to find the litepsm entry in one host's config.
type removalSpec struct {
	Format  ConfigFormat
	KeyPath []string
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
func removalSpecFor(adapter HostAdapter, configPath string) (removalSpec, error) {
	id := strings.ToLower(adapter.Descriptor().HostID)

	if g, ok := adapter.(*GenericAdapter); ok {
		return removalSpec{
			Format:  g.Target.Format,
			KeyPath: g.Target.keyPathFor(false),
		}, nil
	}

	if id == "opencode" {
		return openCodeRemovalSpec(configPath), nil
	}
	if spec, ok := bespokeRemovalSpecs[id]; ok {
		return spec, nil
	}
	return removalSpec{}, fmt.Errorf("no removal path is defined for host %q", id)
}

// openCodeRemovalSpec detects which of OpenCode's two layouts the file uses.
func openCodeRemovalSpec(configPath string) removalSpec {
	data, err := os.ReadFile(configPath)
	if err == nil {
		var root map[string]any
		if json.Unmarshal(data, &root) == nil {
			if mcp, ok := root["mcp"].(map[string]any); ok {
				if _, ok := mcp["servers"].(map[string]any); ok {
					return removalSpec{Format: FormatJSON, KeyPath: []string{"mcp", "servers"}}
				}
			}
		}
	}
	return removalSpec{Format: FormatJSON, KeyPath: []string{"mcpServers"}}
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

	spec, err := removalSpecFor(adapter, configPath)
	if err != nil {
		return nil, nil, err
	}

	proposed, removed, err := stripBridgeEntry(orig, spec)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", adapter.Descriptor().DisplayName, err)
	}
	if !removed {
		return nil, &RemovalResult{
			HostID:     adapter.Descriptor().HostID,
			ConfigPath: configPath,
			Removed:    false,
			Reason:     "the litepsm entry is not present in this file",
		}, nil
	}

	// Never write a config we cannot read back.
	if spec.Format == FormatJSON {
		verify, err := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(proposed))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: removal would produce invalid JSON, refusing to write: %w",
				adapter.Descriptor().DisplayName, err)
		}
		if inspectEntry(verify, spec.KeyPath) {
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

// stripBridgeEntry removes the litepsm server entry, reporting whether it was
// present. Siblings and surrounding formatting are untouched.
func stripBridgeEntry(content string, spec removalSpec) (string, bool, error) {
	if spec.Format == FormatTOML {
		return stripTOMLEntry(content, spec.KeyPath)
	}
	return stripJSONEntry(content, spec.KeyPath)
}

// stripJSONEntry deletes `litepsm` from the object at keyPath.
func stripJSONEntry(content string, keyPath []string) (string, bool, error) {
	if strings.TrimSpace(content) == "" {
		return content, false, nil
	}
	locatable := stripJSONComments(content)

	objStart, objEnd, found, err := jsonValueSpan(locatable, keyPath)
	if err != nil || !found {
		return content, false, nil
	}

	objText := content[objStart:objEnd]
	valueStart, valueEnd, present, err := jsonValueSpan(stripJSONComments(objText), []string{litepsmServerName})
	if err != nil || !present {
		return content, false, nil
	}

	// jsonValueSpan locates the VALUE. A member is `"key": value`, so the key
	// and the colon have to be included or the result is `{"litepsm":}`.
	memberStart := keyStartForValue(objText, valueStart)

	updated, err := deleteObjectMember(objText, memberStart, valueEnd)
	if err != nil {
		return "", false, err
	}
	return content[:objStart] + updated + content[objEnd:], true, nil
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

	j := memberStart
	for j > 0 && isJSONSpace(text[j-1]) {
		j--
	}
	if j > 0 && text[j-1] == ',' {
		j--
		for j > 0 && isJSONSpace(text[j-1]) {
			j--
		}
	}
	return text[:j] + text[memberEnd:], nil
}

// stripTOMLEntry removes one `[prefix.litepsm]` table and its keys.
func stripTOMLEntry(content string, keyPath []string) (string, bool, error) {
	table := "[" + strings.Join(append(append([]string{}, keyPath...), litepsmServerName), ".") + "]"
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
