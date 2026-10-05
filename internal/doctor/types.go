package doctor

import "time"

// CheckStatus represents the status of an individual diagnostic check.
type CheckStatus string

const (
	StatusPass CheckStatus = "pass"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
)

// Category identifies the functional domain a check's failure belongs to so the
// CLI can select the documented ARCH/20 §2 exit code instead of collapsing every
// failure into STATE_ERROR. The zero value leaves a failure uncategorized; the
// CLI falls back to STATE_ERROR for backwards compatibility.
type Category string

const (
	CategoryState    Category = "state"
	CategoryCatalog  Category = "catalog"
	CategoryResolve  Category = "resolve"
	CategoryApproval Category = "approval"
	CategoryInstall  Category = "install"
	CategoryProvider Category = "provider"
	CategoryHost     Category = "host"
)

// CheckResult details the outcome of a diagnostic check.
type CheckResult struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Status         CheckStatus    `json:"status"`
	Category       Category       `json:"category,omitempty"`
	Message        string         `json:"message"`
	Recommendation string         `json:"recommendation,omitempty"`
	Details        map[string]any `json:"details,omitempty"`
}

// DoctorReport aggregates all diagnostic check results.
type DoctorReport struct {
	Timestamp     time.Time     `json:"timestamp"`
	OverallStatus CheckStatus   `json:"overallStatus"`
	Checks        []CheckResult `json:"checks"`
	PassedCount   int           `json:"passedCount"`
	WarnCount     int           `json:"warnCount"`
	FailCount     int           `json:"failCount"`
}

// RepairAction defines an automated corrective step.
type RepairAction struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	ActionKind  string `json:"actionKind"`
	Applied     bool   `json:"applied"`
	Error       string `json:"error,omitempty"`
}

// RepairPlan bundles proposed corrective actions.
type RepairPlan struct {
	CreatedAt time.Time      `json:"createdAt"`
	Actions   []RepairAction `json:"actions"`
}
