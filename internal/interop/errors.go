// Package interop implements `litespm import` (ARCH/32 §5): bringing foreign
// package-manager formats into LiteSPM, import-only for v1.
//
// The pipeline, in the only order it may run:
//
//	read file (bounded) → validate (size, depth, JSON) → parse strictly
//	→ normalize to LiteSPM IR (manifest requires + a source-faithful
//	foreign record) → PLAN printed (nothing written) → approval
//	(interactive terminal or --yes) → write the project manifest only.
//
// Three rules this package exists to keep:
//
//  1. No import writes without a plan and approval (ARCH/32 §5, ARCH/15).
//     The plan prints unconditionally before any write; this package never
//     writes anything itself — the caller applies an approved plan.
//  2. A foreign lockfile/manifest is untrusted input: bounded (8 MiB),
//     depth-checked, strictly schema-validated, never executed, never
//     fetched. Path traversal and absolute paths inside the file are
//     rejected with a named error, and no code the file names is run.
//  3. Lossy is reported, never silent: every field the manifest cannot
//     carry is listed under the plan's Skipped/lossy section, and the
//     source-faithful foreign record (publisher/namespace assertions,
//     hashes, transports) is printed rather than flattened away.
//
// Error codes LPSM-IMPORT-001..007 follow the LPSM-COPY-* pattern of
// ARCH/38 §5.7.
package interop

import (
	"errors"
	"fmt"
)

// Named error codes for `litespm import`. Each code names one class of
// refusal so a script (and `help error`, later) can branch on it.
const (
	// ErrCodeSize: the input file exceeds the 8 MiB import bound.
	ErrCodeSize = "LPSM-IMPORT-001"
	// ErrCodeSyntax: malformed JSON, or nesting deeper than the depth bound.
	ErrCodeSyntax = "LPSM-IMPORT-002"
	// ErrCodeUnsafe: a path or identifier inside the file traverses ("..")
	// or is absolute — refused before it can name anything.
	ErrCodeUnsafe = "LPSM-IMPORT-003"
	// ErrCodeSchema: a required field is missing, a field has the wrong
	// type, or the declared schema/format version is unsupported.
	ErrCodeSchema = "LPSM-IMPORT-004"
	// ErrCodeCommandSource: a `command` source in a marketplace manifest —
	// rejected on import (ARCH/32 §5 row 4), never executed.
	ErrCodeCommandSource = "LPSM-IMPORT-005"
	// ErrCodeFormat: the CLI's format token is not one this command ships.
	ErrCodeFormat = "LPSM-IMPORT-006"
	// ErrCodeApproval: a write was requested without an approval.
	ErrCodeApproval = "LPSM-IMPORT-007"
)

// Error is a named interop error: a stable LPSM-IMPORT code plus a detail
// sentence. Errors returned by this package always carry a code or wrap an
// I/O error from reading the (user-named) input path.
type Error struct {
	Code   string
	Detail string
}

// Error implements error.
func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// errorf builds a named *Error.
func errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// CodeOf returns the LPSM-IMPORT code carried by err (directly or through
// wrapping), or "" when err carries none.
func CodeOf(err error) string {
	var ie *Error
	if errors.As(err, &ie) {
		return ie.Code
	}
	return ""
}
