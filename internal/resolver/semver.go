package resolver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var semverVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// ImplicitVersion is the release's version-less pin. A listing that publishes
// no versions still has one published identity: every component id the release
// emits for it embeds this literal (`<listing-id>@discovery#<kind>/<name>`,
// internal/catalogbuild/dataset.go `versionForID`), while the version record's
// own `version` field stays the honest empty string. The resolver selects this
// pin for such listings so the plan carries a value the catalog already
// publishes instead of a synthesized version (0.0.0 must never reach
// plan.Resolved.Version, the plan hash, or an install record).
//
// It is NOT a semver: it compares below every published version, equals only
// itself, and satisfies only constraints that do not name a different version
// (wildcards, or an explicit request for this literal).
const ImplicitVersion = "discovery"

// Version represents a parsed Semantic Version 2.0.0.
//
// Implicit marks the catalog's version-less pin (ImplicitVersion): a value
// with no proven release, ordered below every published version.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	PreRelease string
	Build      string
	Raw        string
	Implicit   bool
}

// String returns the canonical semver representation.
func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.PreRelease != "" {
		s += "-" + v.PreRelease
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// ParseVersion parses a string into a Version struct. The catalog's
// version-less pin (ImplicitVersion) parses to itself with Implicit set; it is
// deliberately NOT valid semver and never masquerades as 0.0.0.
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	if raw == ImplicitVersion {
		return Version{Raw: raw, Implicit: true}, nil
	}
	cleaned := strings.TrimPrefix(raw, "v")
	if cleaned == "" {
		return Version{}, fmt.Errorf("empty version string")
	}
	parts := semverVersionPattern.FindStringSubmatch(cleaned)
	if parts == nil {
		return Version{}, fmt.Errorf("invalid semver version %q", s)
	}
	major, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("major version %q is outside the supported integer range: %w", parts[1], err)
	}
	minor, err := strconv.Atoi(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("minor version %q is outside the supported integer range: %w", parts[2], err)
	}
	patch, err := strconv.Atoi(parts[3])
	if err != nil {
		return Version{}, fmt.Errorf("patch version %q is outside the supported integer range: %w", parts[3], err)
	}

	return Version{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		PreRelease: parts[4],
		Build:      parts[5],
		Raw:        raw,
	}, nil
}

// Compare compares two versions according to SemVer 2.0.0 precedence.
// Returns -1 if v < other, 0 if v == other, 1 if v > other.
//
// The version-less pin (ImplicitVersion) has no proven release: it compares
// equal only to itself and below every published version, so an implicit pin
// can never satisfy a range built from real semver (and never equals 0.0.0).
func (v Version) Compare(other Version) int {
	if v.Implicit || other.Implicit {
		switch {
		case v.Implicit && other.Implicit:
			return 0
		case v.Implicit:
			return -1
		default:
			return 1
		}
	}
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}

	// Pre-release versions have lower precedence than normal version
	if v.PreRelease == "" && other.PreRelease != "" {
		return 1
	}
	if v.PreRelease != "" && other.PreRelease == "" {
		return -1
	}
	if v.PreRelease == "" && other.PreRelease == "" {
		return 0
	}

	// Compare pre-release identifiers dot-separated
	vParts := strings.Split(v.PreRelease, ".")
	oParts := strings.Split(other.PreRelease, ".")
	minLen := len(vParts)
	if len(oParts) < minLen {
		minLen = len(oParts)
	}

	for i := 0; i < minLen; i++ {
		vNumeric := isNumericIdentifier(vParts[i])
		oNumeric := isNumericIdentifier(oParts[i])

		if vNumeric && oNumeric {
			if cmp := compareNumericIdentifiers(vParts[i], oParts[i]); cmp != 0 {
				return cmp
			}
		} else if vNumeric && !oNumeric {
			// Numeric identifiers have lower precedence than non-numeric
			return -1
		} else if !vNumeric && oNumeric {
			return 1
		} else {
			// Lexical comparison
			if vParts[i] != oParts[i] {
				if vParts[i] < oParts[i] {
					return -1
				}
				return 1
			}
		}
	}

	if len(vParts) < len(oParts) {
		return -1
	} else if len(vParts) > len(oParts) {
		return 1
	}

	return 0
}

func isNumericIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareNumericIdentifiers(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Comparator represents an operator and target version (e.g. ">= 1.2.3").
type Comparator struct {
	Operator string // "=", "==", "!=", "<", "<=", ">", ">=", "^", "~"
	Version  Version
}

// Matches checks if a version satisfies this comparator.
func (c Comparator) Matches(v Version) bool {
	cmp := v.Compare(c.Version)

	switch c.Operator {
	case "", "=", "==":
		return cmp == 0
	case "!=":
		return cmp != 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	case "^":
		// Caret: Allow changes that do not modify the left-most non-zero digit
		if c.Version.Major > 0 {
			// ^1.2.3 := >=1.2.3 <2.0.0
			upper := Version{Major: c.Version.Major + 1, Minor: 0, Patch: 0}
			return cmp >= 0 && v.Compare(upper) < 0
		} else if c.Version.Minor > 0 {
			// ^0.2.3 := >=0.2.3 <0.3.0
			upper := Version{Major: 0, Minor: c.Version.Minor + 1, Patch: 0}
			return cmp >= 0 && v.Compare(upper) < 0
		} else {
			// ^0.0.3 := =0.0.3
			return cmp == 0
		}
	case "~":
		// Tilde: Allow patch-level changes if minor is specified: ~1.2.3 := >=1.2.3 <1.3.0
		upper := Version{Major: c.Version.Major, Minor: c.Version.Minor + 1, Patch: 0}
		return cmp >= 0 && v.Compare(upper) < 0
	default:
		return false
	}
}

// Constraint represents a set of comparators combined with logical AND.
type Constraint struct {
	Raw         string
	Comparators []Comparator
	Wildcard    bool
}

// ParseConstraint parses constraint strings like ">=1.0.0 <2.0.0", "^1.2.0", "*", "latest".
func ParseConstraint(s string) (Constraint, error) {
	raw := strings.TrimSpace(s)
	if raw == "" || raw == "*" || raw == "latest" {
		return Constraint{Raw: raw, Wildcard: true}, nil
	}

	// Normalize commas and spaces: ">=1.0.0, <2.0.0" -> [">=1.0.0", "<2.0.0"]
	normalized := strings.ReplaceAll(raw, ",", " ")
	tokens := strings.Fields(normalized)

	var comparators []Comparator
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "*" || token == "latest" {
			continue
		}

		op, verStr := splitOperator(token)
		if verStr == "" && i+1 < len(tokens) {
			// Case where operator and version are separate tokens: e.g. ">=" "1.0.0"
			op = token
			i++
			verStr = tokens[i]
		}

		v, err := ParseVersion(verStr)
		if err != nil {
			return Constraint{}, fmt.Errorf("invalid version %q in constraint %q: %w", verStr, raw, err)
		}

		comparators = append(comparators, Comparator{
			Operator: op,
			Version:  v,
		})
	}

	return Constraint{
		Raw:         raw,
		Comparators: comparators,
		Wildcard:    len(comparators) == 0,
	}, nil
}

func splitOperator(token string) (string, string) {
	ops := []string{">=", "<=", "!=", "==", ">", "<", "=", "^", "~"}
	for _, op := range ops {
		if strings.HasPrefix(token, op) {
			return op, strings.TrimSpace(strings.TrimPrefix(token, op))
		}
	}
	return "=", strings.TrimSpace(token)
}

// Matches checks if a version satisfies all comparators in the constraint.
func (c Constraint) Matches(v Version) bool {
	if c.Wildcard {
		return true
	}
	for _, comp := range c.Comparators {
		if !comp.Matches(v) {
			return false
		}
	}
	return true
}

// Intersect combines two constraints with logical AND.
func (c Constraint) Intersect(other Constraint) Constraint {
	if c.Wildcard {
		return other
	}
	if other.Wildcard {
		return c
	}

	merged := append([]Comparator{}, c.Comparators...)
	merged = append(merged, other.Comparators...)

	return Constraint{
		Raw:         fmt.Sprintf("%s, %s", c.Raw, other.Raw),
		Comparators: merged,
		Wildcard:    false,
	}
}
