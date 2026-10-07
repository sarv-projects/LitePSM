package interop

// parse.go — the untrusted-input gate every parser calls first, plus the
// shared normalization helpers (ids, slugs, safe-path checks).
//
// Nothing in this file touches the network, the filesystem beyond reading
// the one file the user named, or any executable the input mentions.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/resolver"
)

const (
	// MaxImportBytes bounds every foreign input at 8 MiB. A bigger file is
	// refused with ErrCodeSize rather than streamed into memory.
	MaxImportBytes = 8 << 20
	// MaxJSONDepth bounds JSON nesting at 64 containers. A deeper document
	// is refused with ErrCodeSyntax before encoding/json walks it.
	MaxJSONDepth = 64
)

// Parse dispatches validated bytes onto the parser for a normalized format
// token (NormalizeFormat output). Every parser re-runs ValidateInput on its
// own so it is safe to call directly from tests and future callers.
func Parse(format string, data []byte) (*Doc, error) {
	switch format {
	case FormatSkillsLock:
		return ParseSkillsLock(data)
	case FormatServerJSON:
		return ParseServerJSON(data)
	case FormatClaudeMarketplace:
		return ParseClaudeMarketplace(data)
	case FormatCodexMarketplace:
		return ParseCodexMarketplace(data)
	case FormatPlugin:
		return ParsePlugin(data)
	default:
		return nil, errorf(ErrCodeFormat, "unknown format %q (supported: %s)",
			format, strings.Join(AllFormats, ", "))
	}
}

// ReadFileBounded reads path with the size bound enforced before and during
// the read (a file can grow between Stat and Read). Oversize → ErrCodeSize;
// other failures are ordinary I/O errors naming the path.
func ReadFileBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > MaxImportBytes {
		return nil, errorf(ErrCodeSize, "%s is %d bytes; the import bound is %d bytes (8 MiB)",
			path, fi.Size(), int64(MaxImportBytes))
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > MaxImportBytes {
		return nil, errorf(ErrCodeSize, "%s exceeds the %d byte import bound (8 MiB)",
			path, int64(MaxImportBytes))
	}
	return data, nil
}

// ValidateInput is the untrusted-input gate: size bound, nesting-depth
// bound, then well-formedness. It runs before any typed decode, so a
// hostile document is refused before encoding/json descends into it.
func ValidateInput(data []byte) error {
	if len(data) > MaxImportBytes {
		return errorf(ErrCodeSize, "input is %d bytes; the import bound is %d bytes (8 MiB)",
			len(data), int64(MaxImportBytes))
	}
	if err := checkDepth(data, MaxJSONDepth); err != nil {
		return err
	}
	if !json.Valid(data) {
		return errorf(ErrCodeSyntax, "input is not valid JSON")
	}
	return nil
}

// checkDepth walks the raw bytes (string- and escape-aware) and refuses
// nesting deeper than max. It only counts containers; validation of content
// is json.Valid's and the typed decoders' job.
func checkDepth(data []byte, max int) error {
	depth := 0
	inStr := false
	escaped := false
	for _, b := range data {
		if inStr {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inStr = false
			}
			continue
		}
		switch b {
		case '"':
			inStr = true
		case '{', '[':
			depth++
			if depth > max {
				return errorf(ErrCodeSyntax, "JSON nesting deeper than %d containers", max)
			}
		case '}', ']':
			depth--
		}
	}
	return nil
}

// errDetail renders err for embedding inside another named error without
// repeating the LPSM-IMPORT code the inner *Error already carries.
func errDetail(err error) string {
	var ie *Error
	if errors.As(err, &ie) {
		return ie.Detail
	}
	return err.Error()
}

// decodeStrict unmarshals validated bytes and maps failures onto named
// codes: a type mismatch is a schema violation (004), anything else that
// still smells like syntax is 002. The field path in the message comes
// from encoding/json, so it names the offending location.
func decodeStrict(data []byte, v any) error {
	err := json.Unmarshal(data, v)
	if err == nil {
		return nil
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "(top level)"
		}
		return errorf(ErrCodeSchema, "wrong type for %s: JSON %s where the schema expects %s",
			field, typeErr.Value, typeErr.Type)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return errorf(ErrCodeSyntax, "malformed JSON at byte %d: %v", syntaxErr.Offset, err)
	}
	return errorf(ErrCodeSchema, "cannot decode input: %v", err)
}

// decodeRoot decodes the document top level into a generic object so each
// parser can check required keys, catch wrong types early, and report
// unknown fields instead of dropping them silently.
func decodeRoot(data []byte) (map[string]json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := decodeStrict(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errorf(ErrCodeSchema, "input must be a JSON object at the top level")
	}
	return root, nil
}

// requireKey returns the raw value of a required top-level key or a 004
// naming what is missing.
func requireKey(root map[string]json.RawMessage, key string) (json.RawMessage, error) {
	raw, ok := root[key]
	if !ok {
		return nil, errorf(ErrCodeSchema, "required field %q is missing", key)
	}
	return raw, nil
}

// checkKnownKeys reports every key outside known as a plan warning (sorted,
// so output is deterministic) instead of dropping it silently. Unknown keys
// are warnings, not failures: vendor formats add fields across releases,
// and a reported unknown field loses nothing while a hard refusal would
// block an otherwise sound import.
func checkKnownKeys(m map[string]json.RawMessage, known map[string]bool, where string, warnings *[]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		if !known[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		*warnings = append(*warnings, fmt.Sprintf("unknown field %s.%s — reported, not imported", where, k))
	}
}

// checkSafeField refuses any value taken from the foreign file that names a
// location outside its root: a ".." segment (either separator), an absolute
// path (POSIX, Windows drive, or UNC), or a NUL byte. Import never joins
// these values onto a filesystem path, but the refusal keeps every recorded
// id and path inert no matter what a later phase does with it.
func checkSafeField(field, value string) error {
	if strings.ContainsRune(value, 0) {
		return errorf(ErrCodeUnsafe, "%s contains a NUL byte", field)
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") ||
		(len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')) ||
		strings.HasPrefix(value, `\\`) {
		return errorf(ErrCodeUnsafe, "%s is an absolute path (%q); import only accepts paths relative to the file that declares them", field, value)
	}
	for _, seg := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return errorf(ErrCodeUnsafe, "%s contains a path traversal segment (\"..\"): %q", field, value)
		}
	}
	return nil
}

// slug lowercases and folds any character outside [a-z0-9_-] into '-',
// collapsing runs and trimming the ends. The result only ever feeds a
// SourceID segment, never a filesystem path.
func slug(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-':
			b.WriteRune(r)
			prevDash = r == '-'
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// sourceIDFor derives the SourceID segment of an imported id from the
// foreign source a record came from. Namespaces are folded onto the ones
// LiteSPM already uses (git, npm, local); a source type LiteSPM has no
// namespace for lands in `import` so an unproven mapping is never dressed
// up as a registry LiteSPM knows.
func sourceIDFor(namespace, raw string) (domain.SourceID, error) {
	ns := namespace
	switch strings.ToLower(strings.TrimSpace(namespace)) {
	case "github", "gitlab", "git", "gitea", "bitbucket":
		ns = "git"
	case "npm", "node_modules", "nuget":
		ns = "npm"
	case "local", "path":
		ns = "local"
	default:
		ns = "import"
	}
	ns = slug(ns)
	if len(ns) < 3 || len(ns) > 32 {
		ns = "import"
	}
	part := slug(raw)
	if part == "" {
		part = "unknown"
	}
	if len(part) > 64 {
		part = part[:64]
	}
	if len(part) < 3 {
		part += "-src"
	}
	return domain.ParseSourceID(ns + ":" + part)
}

// listingID builds a canonical LiteSPM listing id and proves it parses:
// the final ParseListingID is the backstop that no imported id can carry a
// character outside the id grammar (ARCH/10).
func listingID(kind domain.ListingKind, sourceID string, upstream string) (string, error) {
	sid, err := domain.ParseSourceID(sourceID)
	if err != nil {
		return "", errorf(ErrCodeSchema, "derived source id %q is not a valid LiteSPM source id: %v", sourceID, err)
	}
	id := string(domain.NewListingID(kind, sid, upstream))
	if _, err := domain.ParseListingID(id); err != nil {
		return "", errorf(ErrCodeSchema, "imported id %q is not a valid LiteSPM listing id: %v", id, err)
	}
	return id, nil
}

var (
	hex40   = regexp.MustCompile(`^[a-f0-9]{40}$`)
	hex64   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	shaPref = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// constraintFromRef maps a foreign git ref onto the manifest constraint
// grammar (ARCH/32 §2: semver / commit / digest). A full commit id becomes
// `commit:<sha>`; a ref the resolver parses as a semver constraint is kept
// verbatim; anything else — a branch or tag name — is ALSO kept verbatim,
// with a warning, because dropping the pin would silently loosen the
// import. The warning states plainly that `litespm lock` may refuse a
// constraint its grammar cannot parse.
func constraintFromRef(ref, field string) (string, string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ""
	}
	if hex40.MatchString(ref) {
		return "commit:" + ref, ""
	}
	if _, err := resolver.ParseConstraint(ref); err == nil {
		return ref, ""
	}
	return ref, fmt.Sprintf(
		"%s %q is a branch or tag name, not a semver or commit constraint; recorded verbatim — `litespm lock` may refuse it",
		field, ref)
}
