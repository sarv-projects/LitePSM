package source

import (
	"encoding/json"
	"path"
	"strings"
)

// flex.go — tolerant JSON helpers for vendor marketplace manifests.
//
// Vendor manifests disagree on small but important shapes: `author` may be a
// string or an object, `category` may be a string or a list, and `source` may
// be a local-path string or a fetch descriptor object. These helpers accept
// every shape observed in the wild and normalize to one form. Unknown shapes
// decode to zero values rather than failing the whole manifest.

// FlexString accepts a JSON string or an object carrying a "name" field.
type FlexString struct {
	Value string
}

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.Value = s
		return nil
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	f.Value = obj.Name
	return nil
}

// String returns the normalized value.
func (f FlexString) String() string { return f.Value }

// FlexStrings accepts a JSON string, an array of strings, or null.
type FlexStrings struct {
	Values []string
}

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexStrings) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s != "" {
			f.Values = []string{s}
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	f.Values = list
	return nil
}

// FlexSource accepts either a local-path string (e.g. "./plugins/foo") or a
// fetch descriptor object. Object keys vary by vendor: the kind key may be
// "source" or "type", the URL key may be "url" or "repo", and the subpath key
// may be "path" or "subdir". All variants are normalized here.
type FlexSource struct {
	// Raw holds the original string when the string form was used.
	Raw string
	// Kind is normalized: github, git-subdir, archive, npm, url, local,
	// command, or unknown.
	Kind    string
	URL     string
	Subpath string
	Ref     string
	Package string
	Command string
	SHA     string
}

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexSource) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.Raw = s
		f.Kind = "local"
		if s == "" {
			f.Kind = "unknown"
		}
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if raw, ok := obj[k]; ok {
				var v string
				if err := json.Unmarshal(raw, &v); err == nil && v != "" {
					return v
				}
			}
		}
		return ""
	}
	kind := strings.ToLower(str("source", "type"))
	f.URL = str("url", "repo")
	f.Subpath = str("path", "subdir")
	f.Ref = str("ref")
	f.Package = str("package")
	f.Command = str("command")
	f.SHA = str("sha", "commit_sha", "commitSha")
	switch kind {
	case "github", "git-subdir", "git", "url", "archive", "npm", "local", "command":
		f.Kind = kind
	case "":
		// No kind key: infer local when only a path is present.
		if f.Subpath != "" && f.URL == "" && f.Package == "" {
			f.Kind = "local"
		} else {
			f.Kind = "unknown"
		}
	default:
		f.Kind = "unknown"
	}
	return nil
}

// IsCommand reports whether this source would execute a shell command.
func (f FlexSource) IsCommand() bool {
	return f.Kind == "command" || f.Command != ""
}

// RepositoryURL returns the best upstream repository locator, if any.
func (f FlexSource) RepositoryURL() string {
	return f.URL
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// slugifyName normalizes a display name into a listing-safe slug.
func slugifyName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-.")
}

// skillBaseName reduces a skill-bundle path (e.g. "./skills/frontend-design")
// to its directory base name.
func skillBaseName(p string) string {
	clean := strings.TrimPrefix(strings.TrimSpace(p), "./")
	return slugifyName(path.Base(clean))
}
