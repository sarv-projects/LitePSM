package interop

// skillslock.go — Vercel `skills-lock.json` import (ARCH/32 §5 row 2).
//
// Shape pinned against the published format (vercel-labs/skills
// src/local-lock.ts for the project file, src/skill-lock.ts for the global
// file, and the vercel-labs skills documentation — retrieved 2026-10-07):
//
//	project lock (v1):  {"version":1, "skills": { "<name>": {
//	  "source", "sourceType", "computedHash", optional "sourceUrl",
//	  "ref", "skillPath", "subagents", "wellKnownDigest" }}}
//	global lock (v3):   same map with "skillFolderHash", "installedAt",
//	  "updatedAt", optional "pluginName"/"sourceBaseUrl" instead of
//	  "computedHash".
//
// Mapping (normative): skill entries become ids of the form `skill:…`;
// version/ref → the manifest constraint. The format has no effect or
// permission data — that absence is stated in the lossy section rather
// than papered over. Nothing in the file is executed or fetched: it is
// parsed as data, bounded, and validated.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Known top-level keys of both documented lock versions.
var skillsLockRootKeys = map[string]bool{
	"version": true, "skills": true,
	// global-lock metadata (version 3):
	"dismissed": true, "lastSelectedAgents": true,
}

// Known entry keys across versions 1 and 3. Unknown keys are reported as
// warnings rather than dropped (vendor formats grow fields between
// releases), but every known field is type-checked strictly.
var skillsLockEntryKeys = map[string]bool{
	"source": true, "sourceUrl": true, "ref": true, "sourceType": true,
	"skillPath": true, "computedHash": true, "wellKnownDigest": true,
	"subagents": true, "pluginName": true, "sourceBaseUrl": true,
	"skillFolderHash": true, "installedAt": true, "updatedAt": true,
}

// ParseSkillsLock normalizes a skills-lock.json (project v1) or
// .skill-lock.json (global v3) document.
func ParseSkillsLock(data []byte) (*Doc, error) {
	if err := ValidateInput(data); err != nil {
		return nil, err
	}
	root, err := decodeRoot(data)
	if err != nil {
		return nil, err
	}

	doc := &Doc{Format: FormatSkillsLock}
	checkKnownKeys(root, skillsLockRootKeys, "(top level)", &doc.Warnings)

	rawVersion, err := requireKey(root, "version")
	if err != nil {
		return nil, err
	}
	var version int
	if err := decodeStrict(rawVersion, &version); err != nil {
		return nil, errorf(ErrCodeSchema, "version: %s", errDetail(err))
	}
	if version != 1 && version != 3 {
		return nil, errorf(ErrCodeSchema,
			"unsupported skills-lock version %d (supported: 1 project lock, 3 global lock; refusing to guess at other versions)", version)
	}
	if version == 3 {
		doc.Warnings = append(doc.Warnings,
			"input is a global skills lock (version 3); its entries become project `requires` — scope is a LiteSPM decision, not the file's")
	}

	rawSkills, err := requireKey(root, "skills")
	if err != nil {
		return nil, err
	}
	if string(rawSkills) == "null" {
		return nil, errorf(ErrCodeSchema, "skills must be a JSON object mapping skill name to entry")
	}
	var skills map[string]json.RawMessage
	if err := decodeStrict(rawSkills, &skills); err != nil {
		return nil, err
	}
	if len(skills) == 0 {
		doc.Warnings = append(doc.Warnings, "the lock declares no skills; there is nothing to import")
	}

	// Sorted iteration: which entry errors first must not depend on map
	// order.
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sortStrings(names)

	var hasDigest, hasSubagents, hasUIState bool
	for _, name := range names {
		entry, subagents, err := parseSkillsLockEntry(name, skills[name], version)
		if err != nil {
			return nil, err
		}
		doc.Entries = append(doc.Entries, *entry)
		if entry.Foreign.Digest != "" {
			hasDigest = true
		}
		if subagents {
			hasSubagents = true
		}
	}
	if _, ok := root["dismissed"]; ok {
		hasUIState = true
	}
	if _, ok := root["lastSelectedAgents"]; ok {
		hasUIState = true
	}

	doc.Lossy = append(doc.Lossy,
		"skills-lock.json carries no effect or permission data in any version; there is nothing to map into policy fields (ARCH/32 §5 row 2)",
		"sourceUrl/sourceBaseUrl and install timestamps (installedAt/updatedAt) are validated but have no manifest field; they are not written")
	if hasDigest {
		doc.Lossy = append(doc.Lossy,
			"content hashes (computedHash/skillFolderHash/wellKnownDigest) have no litespm.yml field; reported in the record above, never written")
	}
	if hasSubagents {
		doc.Lossy = append(doc.Lossy,
			"per-agent placement (subagents) is a skills-CLI concept; no manifest field can carry it — reported in the record above")
	}
	if hasUIState {
		doc.Lossy = append(doc.Lossy,
			"lock UI state (dismissed, lastSelectedAgents) is not imported")
	}
	return doc, nil
}

// parseSkillsLockEntry validates and normalizes one skill entry. Its second
// return reports whether the entry declared subagent placement (used for a
// lossy line).
func parseSkillsLockEntry(name string, raw json.RawMessage, version int) (*Entry, bool, error) {
	field := "skills." + name
	// Everything this function can complain about belongs to THIS entry,
	// so it rides on the entry (the plan prefixes it with the id) instead
	// of the document-level warning list.
	var localWarnings []string
	warnings := &localWarnings
	if string(raw) == "null" {
		return nil, false, errorf(ErrCodeSchema, "%s must be a JSON object", field)
	}
	var obj map[string]json.RawMessage
	if err := decodeStrict(raw, &obj); err != nil {
		return nil, false, err
	}
	checkKnownKeys(obj, skillsLockEntryKeys, field, warnings)

	// Safety first: the map key becomes part of the listing id, so a
	// traversal in the key is refused before any id derivation runs.
	if err := checkSafeField(field+" (skill name)", name); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, false, errorf(ErrCodeSchema, "%s: skill name must not be empty", field)
	}

	source, err := requireEntryString(obj, field, "source")
	if err != nil {
		return nil, false, err
	}
	if err := checkSafeField(field+".source", source); err != nil {
		return nil, false, err
	}
	sourceType, err := requireEntryString(obj, field, "sourceType")
	if err != nil {
		return nil, false, err
	}

	str := func(key string) (string, error) {
		raw, ok := obj[key]
		if !ok {
			return "", nil
		}
		var s string
		if err := decodeStrict(raw, &s); err != nil {
			return "", errorf(ErrCodeSchema, "%s.%s: %s", field, key, errDetail(err))
		}
		return s, nil
	}

	skillPath, err := str("skillPath")
	if err != nil {
		return nil, false, err
	}
	if skillPath != "" {
		if err := checkSafeField(field+".skillPath", skillPath); err != nil {
			return nil, false, err
		}
	}
	ref, err := str("ref")
	if err != nil {
		return nil, false, err
	}
	pluginName, err := str("pluginName")
	if err != nil {
		return nil, false, err
	}
	// The remaining optional strings exist only to type-check them: an
	// entry whose installedAt is a number fails, an unknown key warns.
	for _, key := range []string{"sourceUrl", "sourceBaseUrl", "installedAt", "updatedAt"} {
		if _, err := str(key); err != nil {
			return nil, false, err
		}
	}

	// Digest fields: format-checked, reported, never written.
	digest := ""
	if raw, ok := obj["computedHash"]; ok {
		var s string
		if err := decodeStrict(raw, &s); err != nil {
			return nil, false, errorf(ErrCodeSchema, "%s.computedHash: %s", field, errDetail(err))
		}
		if s != "" && !hex64.MatchString(s) {
			return nil, false, errorf(ErrCodeSchema,
				"%s.computedHash is not a 64-character lowercase SHA-256 hex digest", field)
		}
		digest = s
	}
	if raw, ok := obj["skillFolderHash"]; ok {
		var s string
		if err := decodeStrict(raw, &s); err != nil {
			return nil, false, errorf(ErrCodeSchema, "%s.skillFolderHash: %s", field, errDetail(err))
		}
		if s != "" && !hex64.MatchString(s) && !hex40.MatchString(s) {
			return nil, false, errorf(ErrCodeSchema,
				"%s.skillFolderHash is not a git tree SHA (40) or SHA-256 (64) lowercase hex digest", field)
		}
		if digest == "" {
			digest = s
		}
	}
	if raw, ok := obj["wellKnownDigest"]; ok {
		var s string
		if err := decodeStrict(raw, &s); err != nil {
			return nil, false, errorf(ErrCodeSchema, "%s.wellKnownDigest: %s", field, errDetail(err))
		}
		if s != "" && !shaPref.MatchString(s) {
			return nil, false, errorf(ErrCodeSchema,
				"%s.wellKnownDigest must be \"sha256:\" followed by 64 lowercase hex characters", field)
		}
		if digest == "" {
			digest = s
		}
	}
	subagentPlacement := false
	if raw, ok := obj["subagents"]; ok {
		var list []string
		if err := decodeStrict(raw, &list); err != nil {
			return nil, false, errorf(ErrCodeSchema, "%s.subagents: %s", field, errDetail(err))
		}
		for _, sub := range list {
			if err := checkSafeField(field+".subagents", sub); err != nil {
				return nil, false, err
			}
		}
		if len(list) > 0 {
			subagentPlacement = true
			*warnings = append(*warnings, field+": installed for subagent placement "+
				fmt.Sprintf("%q", strings.Join(list, ","))+" (reported only; not written)")
		}
	}

	sid, err := sourceIDFor(sourceType, source)
	if err != nil {
		return nil, false, errorf(ErrCodeSchema, "%s: cannot derive a LiteSPM source id from source %q (type %q): %v",
			field, source, sourceType, err)
	}
	id, err := listingID("skill", string(sid), name)
	if err != nil {
		return nil, false, err
	}

	constraint, warn := constraintFromRef(ref, field)
	if warn != "" {
		*warnings = append(*warnings, warn)
	}
	if constraint == "" {
		*warnings = append(*warnings, field+" declares no version or ref; imported unconstrained (resolves to whatever the catalog publishes)")
	}

	entry := &Entry{
		ID:         id,
		Kind:       "skill",
		Constraint: constraint,
		Foreign: ForeignRecord{
			Source:     source,
			SourceType: sourceType,
			SkillPath:  skillPath,
			Ref:        ref,
			Digest:     digest,
		},
	}
	if pluginName != "" {
		entry.Foreign.Notes = append(entry.Foreign.Notes,
			fmt.Sprintf("belongs to plugin %q (reported only; not written)", pluginName))
	}
	entry.Warnings = localWarnings
	_ = version // entry validation does not differ between v1 and v3
	return entry, subagentPlacement, nil
}

// requireEntryString returns a required, non-empty string field or 004.
func requireEntryString(obj map[string]json.RawMessage, field, key string) (string, error) {
	raw, ok := obj[key]
	if !ok {
		return "", errorf(ErrCodeSchema, "required field %s.%s is missing", field, key)
	}
	var s string
	if err := decodeStrict(raw, &s); err != nil {
		return "", errorf(ErrCodeSchema, "%s.%s: %s", field, key, errDetail(err))
	}
	if strings.TrimSpace(s) == "" {
		return "", errorf(ErrCodeSchema, "required field %s.%s must not be empty", field, key)
	}
	return s, nil
}
