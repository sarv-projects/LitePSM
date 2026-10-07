package lockfile

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse reads the canonical TOML lock form. It does not verify the digest;
// call Verify (or use Load) for that.
func Parse(data []byte) (*Lock, error) {
	l := &Lock{}
	var cur *LockedEntry
	flush := func() {
		if cur != nil {
			l.Entries = append(l.Entries, *cur)
			cur = nil
		}
	}
	for ln, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripLockComment(raw))
		if line == "" {
			continue
		}
		if line == "[[resolved]]" {
			flush()
			e := LockedEntry{
				SignatureScheme: SchemeNone,
				SignatureResult: ResultUnavailable,
				License:         LicenseNoAssertion,
			}
			cur = &e
			continue
		}
		if strings.HasPrefix(line, "[") {
			return nil, fmt.Errorf("line %d: unexpected section %q", ln+1, line)
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", ln+1)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Header and footer keys are top-level regardless of position:
		// MarshalCanonical emits lockDigest after the [[resolved]] blocks.
		switch key {
		case "lockVersion":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: bad lockVersion", ln+1)
			}
			l.LockVersion = n
			continue
		case "schemaVersion":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: bad schemaVersion", ln+1)
			}
			l.SchemaVersion = n
			continue
		case "manifestDigest":
			l.ManifestDigest = unquoteLock(val)
			continue
		case "lockDigest":
			l.LockDigest = unquoteLock(val)
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: unknown lock key %q", ln+1, key)
		}
		switch key {
		case "id":
			cur.ID = unquoteLock(val)
		case "component":
			cur.Components = append(cur.Components, unquoteLock(val))
		case "sourceIdentity":
			cur.SourceIdentity = unquoteLock(val)
		case "sourceUrl":
			cur.SourceURL = unquoteLock(val)
		case "constraint":
			cur.Constraint = unquoteLock(val)
		case "version":
			cur.Version = unquoteLock(val)
		case "commit":
			cur.Commit = unquoteLock(val)
		case "artifactType":
			cur.ArtifactType = unquoteLock(val)
		case "artifactLocator":
			cur.ArtifactLocator = unquoteLock(val)
		case "artifactSha256":
			cur.ArtifactSHA256 = unquoteLock(val)
		case "artifactSize":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: bad artifactSize", ln+1)
			}
			cur.ArtifactSize = n
		case "treeDigest":
			cur.TreeDigest = unquoteLock(val)
		case "publisherName":
			cur.PublisherName = unquoteLock(val)
		case "signatureScheme":
			cur.SignatureScheme = unquoteLock(val)
		case "signatureResult":
			cur.SignatureResult = unquoteLock(val)
		case "license":
			cur.License = unquoteLock(val)
		case "transport":
			cur.Transport = unquoteLock(val)
		case "policyDigest":
			cur.PolicyDigest = unquoteLock(val)
		case "planDigest":
			cur.PlanDigest = unquoteLock(val)
		case "target":
			cur.Targets = append(cur.Targets, unquoteLock(val))
		default:
			return nil, fmt.Errorf("line %d: unknown entry key %q", ln+1, key)
		}
	}
	flush()
	if l.LockVersion != LockVersion {
		return nil, fmt.Errorf("unsupported lockVersion %d (want %d)", l.LockVersion, LockVersion)
	}
	return l, nil
}

func stripLockComment(line string) string {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inStr = !inStr
		case '#':
			if !inStr {
				return line[:i]
			}
		}
	}
	return line
}

func unquoteLock(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	return s
}

func strconv_quote(s string) string { return strconv.Quote(s) }
