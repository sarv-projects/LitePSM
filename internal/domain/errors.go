package domain

import (
	"encoding/json"
	"fmt"
)

// LPSMError represents a canonical, machine-readable structured error.
// It strictly adheres to schemas/errors.schema.json.
type LPSMError struct {
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Category      string         `json:"category"`
	Retryable     bool           `json:"retryable"`
	CorrelationID string         `json:"correlationId,omitempty"`
	CauseCode     string         `json:"causeCode,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

// Error implements the standard error interface.
func (e *LPSMError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Details != nil && len(e.Details) > 0 {
		return fmt.Sprintf("[%s] %s (category: %s, details: %+v)", e.Code, e.Message, e.Category, e.Details)
	}
	return fmt.Sprintf("[%s] %s (category: %s)", e.Code, e.Message, e.Category)
}

// JSON returns the serialized JSON representation of the error.
func (e *LPSMError) JSON() ([]byte, error) {
	return json.Marshal(e)
}

// WithCorrelation sets the correlation ID on the error.
func (e *LPSMError) WithCorrelation(correlationID string) *LPSMError {
	clone := *e
	clone.CorrelationID = correlationID
	return &clone
}

// WithDetail adds a key-value detail to the error.
func (e *LPSMError) WithDetail(key string, value any) *LPSMError {
	clone := *e
	if clone.Details == nil {
		clone.Details = make(map[string]any)
	} else {
		newDetails := make(map[string]any, len(clone.Details)+1)
		for k, v := range clone.Details {
			newDetails[k] = v
		}
		clone.Details = newDetails
	}
	clone.Details[key] = value
	return &clone
}

// Standard Error Constructors

func ErrInvalidIdentifier(raw, expectedGrammar string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-DOMAIN-INVALID-ID",
		Message:   fmt.Sprintf("invalid identifier %q: expected %s", raw, expectedGrammar),
		Category:  "LPSM-DOMAIN",
		Retryable: false,
		Details: map[string]any{
			"raw":             raw,
			"expectedGrammar": expectedGrammar,
		},
	}
}

func ErrPlanStale(planID, reason string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-PLAN-STALE",
		Message:   fmt.Sprintf("plan %s is stale: %s", planID, reason),
		Category:  "LPSM-PLAN",
		Retryable: true,
		Details: map[string]any{
			"planId": planID,
			"reason": reason,
		},
	}
}

func ErrPlanExpired(planID string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-PLAN-EXPIRED",
		Message:   fmt.Sprintf("plan %s has expired", planID),
		Category:  "LPSM-PLAN",
		Retryable: true,
		Details: map[string]any{
			"planId": planID,
		},
	}
}

func ErrSchemaDrift(capabilityID, expectedFP, actualFP string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-PROVIDER-SCHEMA-DRIFT",
		Message:   fmt.Sprintf("schema drift detected for capability %s", capabilityID),
		Category:  "LPSM-PROVIDER",
		Retryable: false,
		Details: map[string]any{
			"capabilityId": capabilityID,
			"expected":     expectedFP,
			"actual":       actualFP,
		},
	}
}

func ErrResolveConflict(listingID, reason string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-RESOLVE-CONFLICT",
		Message:   fmt.Sprintf("dependency resolution conflict for %s: %s", listingID, reason),
		Category:  "LPSM-RESOLVER",
		Retryable: false,
		Details: map[string]any{
			"listingId": listingID,
			"reason":    reason,
		},
	}
}

func ErrResolveCycle(cyclePath string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-RESOLVE-CYCLE",
		Message:   fmt.Sprintf("dependency cycle detected: %s", cyclePath),
		Category:  "LPSM-RESOLVER",
		Retryable: false,
		Details: map[string]any{
			"cycle": cyclePath,
		},
	}
}

func ErrApprovalConsumed(approvalID string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-POLICY-APPROVAL-CONSUMED",
		Message:   fmt.Sprintf("approval %s has already been consumed (replay prevented)", approvalID),
		Category:  "LPSM-POLICY",
		Retryable: false,
		Details: map[string]any{
			"approvalId": approvalID,
		},
	}
}

func ErrApprovalExpired(approvalID string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-POLICY-APPROVAL-EXPIRED",
		Message:   fmt.Sprintf("approval %s has expired", approvalID),
		Category:  "LPSM-POLICY",
		Retryable: false,
		Details: map[string]any{
			"approvalId": approvalID,
		},
	}
}

func ErrUnauthorized(action, detail string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-POLICY-UNAUTHORIZED",
		Message:   fmt.Sprintf("unauthorized action %s: %s", action, detail),
		Category:  "LPSM-POLICY",
		Retryable: false,
		Details: map[string]any{
			"action": action,
			"detail": detail,
		},
	}
}

func ErrNotFound(entityType, id string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-STATE-NOT-FOUND",
		Message:   fmt.Sprintf("%s %s not found", entityType, id),
		Category:  "LPSM-STATE",
		Retryable: false,
		Details: map[string]any{
			"entityType": entityType,
			"id":         id,
		},
	}
}

func ErrStateConflict(msg string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-STATE-CONFLICT",
		Message:   msg,
		Category:  "LPSM-STATE",
		Retryable: false,
	}
}

func ErrChecksumMismatch(expected, actual string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-VERIFY-CHECKSUM-MISMATCH",
		Message:   fmt.Sprintf("checksum mismatch: expected %s, got %s", expected, actual),
		Category:  "LPSM-VERIFY",
		Retryable: false,
		Details: map[string]any{
			"expected": expected,
			"actual":   actual,
		},
	}
}

func ErrArchiveSlip(path string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-CAS-ARCHIVE-SLIP",
		Message:   fmt.Sprintf("illegal relative path or zip slip attempt: %s", path),
		Category:  "LPSM-CAS",
		Retryable: false,
		Details: map[string]any{
			"path": path,
		},
	}
}

func ErrHostConfigNotFound(hostID, searchedPaths string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-HOST-CONFIG-NOT-FOUND",
		Message:   fmt.Sprintf("host configuration not found for %s (searched: %s)", hostID, searchedPaths),
		Category:  "LPSM-HOST",
		Retryable: false,
		Details: map[string]any{
			"hostId":        hostID,
			"searchedPaths": searchedPaths,
		},
	}
}

func ErrDaemonUnreachable(pipeOrSocketPath string) *LPSMError {
	return &LPSMError{
		Code:      "LPSM-IPC-DAEMON-UNREACHABLE",
		Message:   fmt.Sprintf("unable to connect to LitePSM daemon at %s", pipeOrSocketPath),
		Category:  "LPSM-IPC",
		Retryable: true,
		Details: map[string]any{
			"path": pipeOrSocketPath,
		},
	}
}

func ErrInternal(msg string, cause error) *LPSMError {
	err := &LPSMError{
		Code:      "LPSM-CORE-INTERNAL",
		Message:   msg,
		Category:  "LPSM-CORE",
		Retryable: false,
	}
	if cause != nil {
		err.CauseCode = cause.Error()
	}
	return err
}

// IsRetryable determines if an error allows immediate or exponential retry.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if lpsmErr, ok := err.(*LPSMError); ok {
		return lpsmErr.Retryable
	}
	return false
}

// ErrorCode returns the machine code for LPSMError or empty string.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if lpsmErr, ok := err.(*LPSMError); ok {
		return lpsmErr.Code
	}
	return ""
}
