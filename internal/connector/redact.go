package connector

// redact.go — log and transcript redaction keyed off the manifest's secret
// flags, not off value matching.
//
// Value matching ("did this string appear in the output?") fails exactly when
// it matters: truncated tokens, reformatted JSON, base64 variants. Instead the
// executor knows WHICH fields are secret from the manifest and the auth scheme,
// and redacts by field path before anything reaches a log, a transcript, or an
// agent-visible result.
//
// The placeholder is stable (`:censored:<sha256-prefix>`) so operators can
// correlate "the same secret appeared twice" without seeing any of it.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// CensoredPlaceholder returns a stable, non-reversible placeholder for one
// secret value. The first 8 hex chars of the SHA-256 are enough to correlate
// occurrences within one operator session and reveal nothing usable.
func CensoredPlaceholder(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return ":censored:" + hex.EncodeToString(sum[:])[:8]
}

// RedactFields returns a copy of record with every dotted field path replaced
// by its placeholder. Paths use dot notation ("headers.Authorization",
// "body.access_token"). Missing paths are ignored — a connector that stops
// returning a field must not break logging.
func RedactFields(record map[string]any, secretPaths []string, secretFor func(path string) (string, bool)) map[string]any {
	out := deepCopyMap(record)
	for _, path := range secretPaths {
		redactPath(out, strings.Split(path, "."), secretFor, path)
	}
	return out
}

func redactPath(node map[string]any, parts []string, secretFor func(path string) (string, bool), full string) {
	if len(parts) == 0 {
		return
	}
	key := parts[0]
	val, ok := node[key]
	if !ok {
		return
	}
	if len(parts) == 1 {
		if s, isStr := val.(string); isStr && s != "" {
			if secret, known := secretFor(full); known {
				_ = secret
				node[key] = CensoredPlaceholder(s)
			} else {
				node[key] = CensoredPlaceholder(s)
			}
		} else if isSecretShaped(val) {
			node[key] = CensoredPlaceholder(fmt.Sprint(val))
		}
		return
	}
	child, ok := val.(map[string]any)
	if !ok {
		return
	}
	redactPath(child, parts[1:], secretFor, full)
}

// isSecretShaped catches non-string values that must never be logged raw
// (nested token objects that slipped through field lists).
func isSecretShaped(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k := range t {
			lower := strings.ToLower(k)
			if lower == "access_token" || lower == "refresh_token" || lower == "token" {
				return true
			}
		}
	case []byte:
		return len(t) > 0
	}
	return false
}

func deepCopyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case map[string]any:
			out[k] = deepCopyMap(t)
		case []any:
			cp := make([]any, len(t))
			for i, item := range t {
				if m, ok := item.(map[string]any); ok {
					cp[i] = deepCopyMap(m)
				} else {
					cp[i] = item
				}
			}
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}

// AuditEvent is the field-level audit record emitted per credential use.
// Field-level, never value-level: who used which credential field against
// which host, when, and what happened.
type AuditEvent struct {
	ConnectorID  string `json:"connectorId"`
	ConnectionID string `json:"connectionId"`
	Field        string `json:"field"`
	Host         string `json:"host"`
	Operation    string `json:"operation"`
	Outcome      string `json:"outcome"`
}

// FormatAuditEvent renders one audit line with no secret material. The format
// is fixed so log scrapers can rely on it; values are never interpolated.
func FormatAuditEvent(e AuditEvent) string {
	return fmt.Sprintf("connector=%s connection=%s field=%s host=%s op=%s outcome=%s",
		e.ConnectorID, e.ConnectionID, e.Field, e.Host, e.Operation, e.Outcome)
}
