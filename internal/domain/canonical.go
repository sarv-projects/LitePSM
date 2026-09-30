package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ComputePlanHash computes the SHA-256 digest over the RFC 8785 canonical JSON
// of all execution-relevant fields of an InstallPlan.
// Volatile fields (planId, createdAt, expiresAt) are explicitly excluded.
func ComputePlanHash(plan *InstallPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("cannot compute planHash of nil plan")
	}

	execObj := map[string]any{
		"schemaVersion":    plan.SchemaVersion,
		"catalogReleaseId": plan.CatalogReleaseID,
		"request":          plan.Request,
		"resolved":         plan.Resolved,
		"effects":          plan.Effects,
		"preconditions":    plan.Preconditions,
	}
	if plan.SourceSnapshots != nil {
		execObj["sourceSnapshots"] = plan.SourceSnapshots
	}
	if plan.HostChanges != nil {
		execObj["hostChanges"] = plan.HostChanges
	}
	if plan.ProviderLaunches != nil {
		execObj["providerLaunches"] = plan.ProviderLaunches
	}
	if plan.RequestedAccess != nil {
		execObj["requestedAccess"] = plan.RequestedAccess
	}

	rawJSON, err := json.Marshal(execObj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal plan execution fields: %w", err)
	}

	canonicalJSON, err := CanonicalizeJSON(rawJSON)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize plan JSON: %w", err)
	}

	hash := sha256.Sum256(canonicalJSON)
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(hash[:])), nil
}

// ComputeSchemaFingerprint computes the SHA-256 digest of the canonical JSON of a tool schema.
func ComputeSchemaFingerprint(jsonSchema []byte) (string, error) {
	if len(jsonSchema) == 0 {
		return "", fmt.Errorf("jsonSchema cannot be empty")
	}

	canonicalJSON, err := CanonicalizeJSON(jsonSchema)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize schema JSON: %w", err)
	}

	hash := sha256.Sum256(canonicalJSON)
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(hash[:])), nil
}

// ComputeDigest streams data from reader and computes sha256:<hex>.
func ComputeDigest(r io.Reader) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, r); err != nil {
		return "", fmt.Errorf("failed to compute digest from reader: %w", err)
	}
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(hasher.Sum(nil))), nil
}

// ComputeBytesDigest computes sha256:<hex> of a byte slice.
func ComputeBytesDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(hash[:]))
}

// CanonicalizeJSON implements RFC 8785 (JSON Canonicalization Scheme - JCS).
// It accepts any valid JSON bytes and returns canonicalized JSON bytes.
func CanonicalizeJSON(input []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(input))
	d.UseNumber()

	var val any
	if err := d.Decode(&val); err != nil {
		return nil, fmt.Errorf("invalid JSON input: %w", err)
	}

	// Ensure there is no trailing trailing invalid data
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after JSON document")
	}

	var buf bytes.Buffer
	if err := formatCanonical(val, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func formatCanonical(v any, buf *bytes.Buffer) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil

	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil

	case string:
		formatCanonicalString(val, buf)
		return nil

	case json.Number:
		// Format number according to ECMAScript / RFC 8785 specification
		return formatCanonicalNumber(val.String(), buf)

	case []any:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := formatCanonical(item, buf); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil

	case map[string]any:
		buf.WriteByte('{')

		// RFC 8785 Section 3.2.3: Keys must be sorted by UTF-16 code units
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sortUTF16(keys)

		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			formatCanonicalString(k, buf)
			buf.WriteByte(':')
			if err := formatCanonical(val[k], buf); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil

	default:
		return fmt.Errorf("unsupported type for canonical JSON: %T", v)
	}
}

// formatCanonicalString writes a JSON-escaped string complying with RFC 8785.
func formatCanonicalString(s string, buf *bytes.Buffer) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			buf.WriteString(`\\`)
		case '"':
			buf.WriteString(`\"`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// formatCanonicalNumber ensures number representation complies with RFC 8785.
func formatCanonicalNumber(numStr string, buf *bytes.Buffer) error {
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return fmt.Errorf("invalid number: %s", numStr)
	}

	// -0 must be serialized as 0
	if f == 0 {
		buf.WriteString("0")
		return nil
	}

	// Format float with ECMAScript 64-bit IEEE 754 precision
	formatted := strconv.FormatFloat(f, 'g', -1, 64)

	// In ES/RFC 8785, exponent must be lowercase 'e' and cannot have a redundant '+' sign
	formatted = strings.ReplaceAll(formatted, "E+", "e+")
	formatted = strings.ReplaceAll(formatted, "E-", "e-")
	formatted = strings.ReplaceAll(formatted, "E", "e")
	formatted = strings.ReplaceAll(formatted, "e+", "e")

	buf.WriteString(formatted)
	return nil
}

// sortUTF16 sorts strings lexicographically by their UTF-16 code units as required by RFC 8785.
func sortUTF16(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		return compareUTF16(keys[i], keys[j]) < 0
	})
}

// compareUTF16 compares two strings by UTF-16 code units.
func compareUTF16(a, b string) int {
	rA := []rune(a)
	rB := []rune(b)
	lenA := len(rA)
	lenB := len(rB)
	minLen := lenA
	if lenB < minLen {
		minLen = lenB
	}

	for idx := 0; idx < minLen; idx++ {
		cpA := rA[idx]
		cpB := rB[idx]
		if cpA == cpB {
			continue
		}

		u16A := toUTF16Units(cpA)
		u16B := toUTF16Units(cpB)

		minUnits := len(u16A)
		if len(u16B) < minUnits {
			minUnits = len(u16B)
		}
		for uIdx := 0; uIdx < minUnits; uIdx++ {
			if u16A[uIdx] != u16B[uIdx] {
				if u16A[uIdx] < u16B[uIdx] {
					return -1
				}
				return 1
			}
		}
		if len(u16A) != len(u16B) {
			if len(u16A) < len(u16B) {
				return -1
			}
			return 1
		}
	}

	if lenA < lenB {
		return -1
	} else if lenA > lenB {
		return 1
	}
	return 0
}

func toUTF16Units(r rune) []uint16 {
	if r <= 0xFFFF {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	lead := uint16(0xD800 + (r >> 10))
	trail := uint16(0xDC00 + (r & 0x3FF))
	return []uint16{lead, trail}
}

// EnsureValidUTF8 checks if a byte slice is valid UTF-8.
func EnsureValidUTF8(b []byte) bool {
	return utf8.Valid(b)
}
