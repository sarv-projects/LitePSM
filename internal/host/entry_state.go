package host

// entry_state.go — reading back, fingerprinting and surgically removing (or
// restoring) one owned MCP server entry.
//
// The deployment ledger (ARCH/33) needs three facts about an owned node:
// what LiteSPM wrote (fingerprint of the entry as read back from disk), what
// is there now (same function, later), and what was there before (the prior
// entry, when an install replaced a user-authored one). Fingerprints are taken
// over the entry's canonical text, never over the whole file, so sibling edits
// elsewhere in the config never look like a modification of our node.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/fslock"
)

// EntryState is one server entry as currently present in a host config.
type EntryState struct {
	HostID     string
	ConfigPath string
	// Raw is the entry's canonical text: compact key-sorted JSON for JSON
	// hosts, the trimmed `[table]` block for TOML hosts.
	Raw string
	// Fingerprint is "sha256:" + hex(sha256(Raw)).
	Fingerprint string
}

func fingerprintOf(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// entryStateFromContent extracts the named entry from config text. present is
// false when the entry does not exist; an unparseable JSON document is an error.
func entryStateFromContent(spec entrySpec, content, name string) (raw string, present bool, err error) {
	if strings.TrimSpace(content) == "" {
		return "", false, nil
	}
	if spec.Format == FormatTOML {
		header := "[" + strings.Join(append(append([]string{}, spec.KeyPath...), name), ".") + "]"
		var block []string
		in := false
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == header {
				in = true
				block = append(block, trimmed)
				continue
			}
			if in {
				if strings.HasPrefix(trimmed, "[") {
					break
				}
				if trimmed != "" {
					block = append(block, trimmed)
				}
			}
		}
		if !in {
			return "", false, nil
		}
		return strings.Join(block, "\n"), true, nil
	}
	root, perr := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(content))
	if perr != nil {
		return "", false, perr
	}
	value, ok := lookupInMap(root, spec.KeyPath)[name]
	if !ok {
		return "", false, nil
	}
	b, merr := json.Marshal(value)
	if merr != nil {
		return "", false, merr
	}
	return string(b), true, nil
}

// ReadServerEntryState reads the named entry from a host's config. It returns
// (nil, nil) when the config file or the entry does not exist.
func ReadServerEntryState(ctx context.Context, hostID, name string, scope domain.InstallScope) (*EntryState, error) {
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return nil, err
	}
	spec, ok := entrySpecForScope(adapter, scope)
	if !ok {
		return nil, fmt.Errorf("host %q has no documented MCP entry shape", hostID)
	}
	configPath, err := adapter.DetectConfig(ctx, scope)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	raw, present, err := entryStateFromContent(spec, string(data), name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", adapter.Descriptor().DisplayName, err)
	}
	if !present {
		return nil, nil
	}
	return &EntryState{
		HostID:      adapter.Descriptor().HostID,
		ConfigPath:  configPath,
		Raw:         raw,
		Fingerprint: fingerprintOf(raw),
	}, nil
}

// EntryUnreadable is the fingerprint sentinel for a config that cannot be
// parsed or looked up right now. It never equals a real "sha256:..." value, so
// a three-way reconcile classifies the node as user-edited and refuses to
// strip it — never as ours to delete.
const EntryUnreadable = "unreadable:entry"

// EntryFingerprintNow fingerprints the named server entry as it exists in the
// host config at this moment. "" means the config file or the entry is gone
// (a genuine missing); EntryUnreadable means the answer could not be
// established (unparseable document, host without a declared layout), which
// callers must treat as "not provably ours" rather than as missing.
//
// This is the C image of ARCH/33 §4 for a host-config node: it hashes the
// entry's canonical text, never the whole file, so a sibling edit elsewhere in
// the config does not read as modification of this node.
func EntryFingerprintNow(ctx context.Context, hostID, name string, scope domain.InstallScope) (string, error) {
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return EntryUnreadable, err
	}
	spec, ok := entrySpecForScope(adapter, scope)
	if !ok {
		return EntryUnreadable, fmt.Errorf("host %q has no documented MCP entry shape", hostID)
	}
	configPath, err := adapter.DetectConfig(ctx, scope)
	if err != nil {
		return EntryUnreadable, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return EntryUnreadable, err
	}
	raw, present, err := entryStateFromContent(spec, string(data), name)
	if err != nil {
		return EntryUnreadable, nil
	}
	if !present {
		return "", nil
	}
	return fingerprintOf(raw), nil
}

// EntryRemoveResult reports what RemoveServerEntry did to one host config.
type EntryRemoveResult struct {
	HostID     string
	ConfigPath string
	BackupPath string
	// Removed is true when an entry was deleted or replaced by the restore.
	Removed bool
	// Restored is true when the prior (user-authored) entry was put back.
	Restored bool
	Reason   string
}

// RemoveServerEntry deletes the named server entry from a host config, touching
// nothing else. When restorePrior is non-empty (the canonical text recorded by
// InstallServerEntry as EntryInstallResult.PriorEntry), the entry is replaced by
// that prior value instead of deleted, so an install that overwrote a
// user-authored entry is fully reversed. An entry that is already gone is
// reported as Removed=false, not as an error.
func RemoveServerEntry(ctx context.Context, hostID, name string, scope domain.InstallScope, backupDir, restorePrior string) (*EntryRemoveResult, error) {
	if err := ValidateServerEntryName(name); err != nil {
		return nil, err
	}
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return nil, err
	}
	spec, ok := entrySpecForScope(adapter, scope)
	if !ok {
		return nil, fmt.Errorf("host %q has no documented MCP entry shape", hostID)
	}
	configPath, err := adapter.DetectConfig(ctx, scope)
	if err != nil {
		return nil, err
	}
	configLock, err := fslock.Acquire(configPath+hostConfigWriteLockSuffix, fslock.Options{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("lock host config %s: %w", configPath, err)
	}
	defer func() { _ = configLock.Release() }()
	res := &EntryRemoveResult{HostID: adapter.Descriptor().HostID, ConfigPath: configPath}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			res.Reason = "no config file at this path"
			return res, nil
		}
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	originalBytes := append([]byte(nil), data...)
	original := string(data)

	// Try the container the install wrote to first, then every documented
	// alternative (OpenCode has three layouts).
	paths := [][]string{spec.KeyPath}
	if rs, rerr := removalSpecFor(adapter); rerr == nil {
		for _, c := range rs.effectiveKeyPaths() {
			if strings.Join(c, ".") != strings.Join(spec.KeyPath, ".") {
				paths = append(paths, c)
			}
		}
	}

	var proposed string
	found := false
	var firstErr error
	for _, kp := range paths {
		attemptSpec := spec
		attemptSpec.KeyPath = kp
		if _, present, perr := entryStateFromContent(attemptSpec, original, name); perr != nil {
			if firstErr == nil {
				firstErr = perr
			}
			continue
		} else if !present {
			continue
		}
		if restorePrior != "" {
			restored, rerr := restoreEntry(attemptSpec, original, name, restorePrior)
			if rerr != nil {
				return nil, fmt.Errorf("%s: restoring prior entry %q: %w", adapter.Descriptor().HostID, name, rerr)
			}
			proposed, found = restored, true
			res.Restored = true
		} else {
			stripped, did, serr := stripServerEntry(original, removalSpec{Format: spec.Format, KeyPath: kp}, name)
			if serr != nil {
				return nil, fmt.Errorf("%s: %w", adapter.Descriptor().HostID, serr)
			}
			if !did {
				continue
			}
			proposed, found = stripped, true
		}
		break
	}
	if !found {
		if firstErr != nil {
			return nil, fmt.Errorf("%s: %w", adapter.Descriptor().DisplayName, firstErr)
		}
		res.Reason = fmt.Sprintf("entry %q is not present in this file", name)
		return res, nil
	}

	if spec.Format == FormatJSON {
		if _, perr := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(proposed)); perr != nil {
			return nil, fmt.Errorf("%s: removal would produce invalid JSON, refusing to write: %w", adapter.Descriptor().HostID, perr)
		}
	}
	backupPath, err := CreateAtomicBackup(configPath, backupDir, adapter.Descriptor().HostID)
	if err != nil {
		return nil, err
	}
	if err := AtomicWriteFileIfUnchanged(configPath, []byte(proposed), 0600, originalBytes, true); err != nil {
		return nil, fmt.Errorf("write %s: %w", configPath, err)
	}
	res.BackupPath = backupPath
	res.Removed = true
	return res, nil
}

// restoreEntry replaces the named entry with prior canonical text.
func restoreEntry(spec entrySpec, content, name, prior string) (string, error) {
	if spec.Format == FormatTOML {
		header := "[" + strings.Join(append(append([]string{}, spec.KeyPath...), name), ".") + "]"
		return mergeTOMLEntry(content, header, prior+"\n")
	}
	var value any
	if err := json.Unmarshal([]byte(prior), &value); err != nil {
		return "", fmt.Errorf("recorded prior entry is not valid JSON: %w", err)
	}
	return mergeJSONEntrySurgicalNamed(content, spec.KeyPath, name, value)
}
