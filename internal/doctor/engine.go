package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/artifact"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/secrets"
	"github.com/sarv-projects/litespm/internal/state"
)

// Engine executes system diagnostic verification.
type Engine struct {
	paths       *config.PlatformPaths
	db          *state.DB
	secretStore secrets.SecretStore
}

// NewEngine creates a diagnostic Engine.
func NewEngine(paths *config.PlatformPaths, db *state.DB, store secrets.SecretStore) *Engine {
	return &Engine{
		paths:       paths,
		db:          db,
		secretStore: store,
	}
}

// checkCategories maps each built-in diagnostic check ID to the functional
// domain of its failure, which the CLI resolves to the ARCH/20 §2 exit code.
// The mapping is by ID so every failure return path of a check carries the same
// category without repeating it at each return site.
//
// Rationale per check:
//   - directories, database, journal, disk: local state/storage conditions
//     (STATE_ERROR, 70) — the repair path recreates storage or frees capacity.
//   - CAS store, staging: install artifacts left behind/corrupted
//     (INSTALL_ERROR, 40).
//   - secret vault, runtimes: provider/runtime or credential-vault access
//     (PROVIDER_ERROR, 50; the table's closest class names auth failure).
//   - host registrations, host backups: agent configuration files
//     (HOST_ERROR, 60).
var checkCategories = map[string]Category{
	"check_dirs":     CategoryState,
	"check_db":       CategoryState,
	"check_journal":  CategoryState,
	"check_cas":      CategoryInstall,
	"check_staging":  CategoryInstall,
	"check_secrets":  CategoryProvider,
	"check_hosts":    CategoryHost,
	"check_backups":  CategoryHost,
	"check_runtimes": CategoryProvider,
	"check_disk":     CategoryState,
}

// classify tags a check result with the category of its check ID (if known).
func classify(c CheckResult) CheckResult {
	c.Category = checkCategories[c.ID]
	return c
}

// RunChecks executes all 10 system diagnostics.
func (e *Engine) RunChecks(ctx context.Context) *DoctorReport {
	var checks []CheckResult
	now := time.Now().UTC()

	// 1. Check Platform Directories
	checks = append(checks, classify(e.checkDirectories()))

	// 2. Check SQLite State Database
	checks = append(checks, classify(e.checkDatabase(ctx)))

	// 3. Check Incomplete Operation Journal
	checks = append(checks, classify(e.checkOperationJournal(ctx)))

	// 4. Check CAS Store Integrity
	checks = append(checks, classify(e.checkCASStore()))

	// 5. Check Staging Directory Hygiene
	checks = append(checks, classify(e.checkStagingHygiene()))

	// 6. Check OS Secret Store
	checks = append(checks, classify(e.checkSecretVault(ctx)))

	// 7. Check Host Registrations
	checks = append(checks, classify(e.checkHostRegistrations(ctx)))

	// 8. Check Host Backups
	checks = append(checks, classify(e.checkHostBackups()))

	// 9. Check Provider Runtimes
	checks = append(checks, classify(e.checkRuntimes()))

	// 10. Check Disk Availability
	checks = append(checks, classify(e.checkDiskSpace()))

	// Aggregate counts
	passed, warns, fails := 0, 0, 0
	for _, c := range checks {
		switch c.Status {
		case StatusPass:
			passed++
		case StatusWarn:
			warns++
		case StatusFail:
			fails++
		}
	}

	overall := StatusPass
	if warns > 0 {
		overall = StatusWarn
	}
	if fails > 0 {
		overall = StatusFail
	}

	return &DoctorReport{
		Timestamp:     now,
		OverallStatus: overall,
		Checks:        checks,
		PassedCount:   passed,
		WarnCount:     warns,
		FailCount:     fails,
	}
}

// checkDirectories verifies the DATA_ROOT / CONFIG_ROOT / RUNTIME_ROOT layout.
//
// It is deliberately READ-ONLY: a diagnostic must observe the system, never
// change it, or `doctor` and `doctor --repair` stop being distinguishable and a
// report can no longer tell the user what was actually broken. Directories are
// (re)created by the `repair_dirs` action (`--repair`) or by the daemon at
// startup — never by this check.
func (e *Engine) checkDirectories() CheckResult {
	if e.paths == nil {
		return CheckResult{
			ID:      "check_dirs",
			Name:    "Directory Permissions & Structure",
			Status:  StatusFail,
			Message: "platform paths uninitialized",
		}
	}

	roots := []struct {
		label string
		path  string
	}{
		{"DATA_ROOT", e.paths.DataRoot},
		{"CONFIG_ROOT", e.paths.ConfigRoot},
		{"RUNTIME_ROOT", e.paths.RuntimeRoot},
	}

	var missing []string
	for _, root := range roots {
		if root.path == "" {
			continue
		}
		info, err := os.Stat(root.path)
		switch {
		case os.IsNotExist(err):
			missing = append(missing, fmt.Sprintf("%s (%s does not exist)", root.label, root.path))
		case err != nil:
			return CheckResult{
				ID:             "check_dirs",
				Name:           "Directory Permissions & Structure",
				Status:         StatusFail,
				Message:        fmt.Sprintf("cannot stat %s (%s): %v", root.label, root.path, err),
				Recommendation: "Check file system permissions on DATA_ROOT and CONFIG_ROOT.",
			}
		case !info.IsDir():
			return CheckResult{
				ID:             "check_dirs",
				Name:           "Directory Permissions & Structure",
				Status:         StatusFail,
				Message:        fmt.Sprintf("%s is not a directory: %s", root.label, root.path),
				Recommendation: fmt.Sprintf("Move %s aside so it can be recreated as a directory.", root.path),
			}
		}
	}

	if len(missing) > 0 {
		return CheckResult{
			ID:             "check_dirs",
			Name:           "Directory Permissions & Structure",
			Status:         StatusFail,
			Message:        fmt.Sprintf("%d required director%s missing: %s", len(missing), plural(len(missing)), strings.Join(missing, "; ")),
			Recommendation: "Run `litespm doctor --repair` to create the hierarchy, or start the daemon once.",
			Details:        map[string]any{"missing": missing},
		}
	}

	return CheckResult{
		ID:      "check_dirs",
		Name:    "Directory Permissions & Structure",
		Status:  StatusPass,
		Message: fmt.Sprintf("directories verified (read-only check) in %s", e.paths.DataRoot),
	}
}

// plural is the minimal suffix helper for check messages; kept local so the
// package does not grow a strings utility for one call site.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func (e *Engine) checkDatabase(ctx context.Context) CheckResult {
	if e.db == nil {
		return CheckResult{
			ID:             "check_db",
			Name:           "SQLite State Database Integrity",
			Status:         StatusFail,
			Message:        "database connection unavailable",
			Recommendation: "Verify that state.db exists in DATA_ROOT.",
		}
	}

	var integrityResult string
	err := e.db.Raw().QueryRowContext(ctx, "PRAGMA integrity_check;").Scan(&integrityResult)
	if err != nil {
		return CheckResult{
			ID:             "check_db",
			Name:           "SQLite State Database Integrity",
			Status:         StatusFail,
			Message:        fmt.Sprintf("integrity check failed: %v", err),
			Recommendation: "Run litespm doctor --repair or restore state.db from backup.",
		}
	}

	if integrityResult != "ok" {
		return CheckResult{
			ID:             "check_db",
			Name:           "SQLite State Database Integrity",
			Status:         StatusFail,
			Message:        fmt.Sprintf("database corruption detected: %s", integrityResult),
			Recommendation: "Restore state.db from DATA_ROOT/backups/db/.",
		}
	}

	// Measure the journal mode rather than asserting it. The connection string
	// asks for WAL (`internal/state/db.go`), but "WAL mode active" is a claim
	// about the database as it is open right now, so read it back.
	var journalMode string
	if err := e.db.Raw().QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		return CheckResult{
			ID:             "check_db",
			Name:           "SQLite State Database Integrity",
			Status:         StatusWarn,
			Message:        fmt.Sprintf("integrity check ok but journal_mode could not be read: %v", err),
			Recommendation: "Inspect state.db permissions; the database may have been opened by another process.",
		}
	}

	if !strings.EqualFold(journalMode, "wal") {
		return CheckResult{
			ID:      "check_db",
			Name:    "SQLite State Database Integrity",
			Status:  StatusWarn,
			Message: fmt.Sprintf("relational tables verified (PRAGMA integrity_check ok), but journal_mode is %q, not wal", journalMode),
			Details: map[string]any{"journalMode": journalMode},
			Recommendation: "Restart the daemon so the database is reopened with journal_mode=WAL; " +
				"a rollback-journaled database gives up the concurrent-reader guarantee the design assumes.",
		}
	}

	return CheckResult{
		ID:      "check_db",
		Name:    "SQLite State Database Integrity",
		Status:  StatusPass,
		Message: fmt.Sprintf("relational tables verified (PRAGMA integrity_check ok, journal_mode=%s measured)", journalMode),
		Details: map[string]any{"journalMode": journalMode},
	}
}

// checkOperationJournal queries the operations table for rows that never
// reached a terminal state (crash/interrupt leftovers).
func (e *Engine) checkOperationJournal(ctx context.Context) CheckResult {
	if e.db == nil {
		return CheckResult{
			ID:             "check_journal",
			Name:           "Incomplete Operation Journal",
			Status:         StatusWarn,
			Message:        "state database unavailable; operation journal was not inspected",
			Recommendation: "See the SQLite State Database check for the underlying problem.",
		}
	}

	ops, err := e.db.GetNonTerminalOperations(ctx)
	if err != nil {
		return CheckResult{
			ID:             "check_journal",
			Name:           "Incomplete Operation Journal",
			Status:         StatusFail,
			Message:        fmt.Sprintf("failed to query operation journal: %v", err),
			Recommendation: "Run litespm doctor --repair or inspect state.db manually.",
		}
	}

	if len(ops) > 0 {
		ids := make([]string, 0, len(ops))
		states := make(map[string]int, len(ops))
		for _, op := range ops {
			ids = append(ids, op.OperationID)
			states[op.State]++
		}
		return CheckResult{
			ID:      "check_journal",
			Name:    "Incomplete Operation Journal",
			Status:  StatusWarn,
			Message: fmt.Sprintf("%d operation(s) never reached a terminal state", len(ops)),
			Details: map[string]any{
				"nonTerminalCount": len(ops),
				"operationIds":     ids,
				"states":           states,
			},
			Recommendation: "These operations were interrupted; inspect them in state.db (and clean any staging leftovers) before retrying installs.",
		}
	}

	return CheckResult{
		ID:      "check_journal",
		Name:    "Incomplete Operation Journal",
		Status:  StatusPass,
		Message: "no non-terminal operations in the journal",
	}
}

// casVerifyLimit bounds how many CAS trees are re-hashed per doctor run so the
// check stays responsive on large stores. Skipped trees are reported, not
// silently passed.
const casVerifyLimit = 32

func (e *Engine) checkCASStore() CheckResult {
	if e.paths == nil {
		return CheckResult{ID: "check_cas", Name: "CAS Store Integrity", Status: StatusFail, Message: "platform paths uninitialized"}
	}
	treesRoot := filepath.Join(e.paths.CASPath(), "trees", "sha256")
	entries, err := os.ReadDir(treesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{
				ID:             "check_cas",
				Name:           "CAS Store Integrity",
				Status:         StatusWarn,
				Message:        "CAS store has no content trees yet (nothing to verify)",
				Recommendation: "Trees are created on the first install.",
			}
		}
		return CheckResult{
			ID:             "check_cas",
			Name:           "CAS Store Integrity",
			Status:         StatusFail,
			Message:        fmt.Sprintf("failed to read CAS store at %s: %v", treesRoot, err),
			Recommendation: "Check filesystem permissions on DATA_ROOT/cas.",
		}
	}

	if len(entries) == 0 {
		return CheckResult{
			ID:      "check_cas",
			Name:    "CAS Store Integrity",
			Status:  StatusWarn,
			Message: "CAS store contains no content trees yet (nothing to verify)",
			Details: map[string]any{"treeCount": 0},
		}
	}

	verified, mismatched, unreadable, invalid := 0, 0, 0, 0
	skipped := 0
	var problems []string

	for i, entry := range entries {
		if i >= casVerifyLimit {
			skipped = len(entries) - casVerifyLimit
			break
		}
		if !entry.IsDir() || !isLowerHex(entry.Name(), 64) {
			invalid++
			problems = append(problems, fmt.Sprintf("unexpected entry %q", entry.Name()))
			continue
		}
		got, err := artifact.ComputeCanonicalTreeDigest(filepath.Join(treesRoot, entry.Name()))
		if err != nil {
			unreadable++
			problems = append(problems, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}
		if got != "sha256:"+entry.Name() {
			mismatched++
			problems = append(problems, fmt.Sprintf("%s: digest mismatch (content changed since install)", entry.Name()))
			continue
		}
		verified++
	}

	details := map[string]any{
		"treeCount":     len(entries),
		"verifiedCount": verified,
		"skippedCount":  skipped,
	}
	if mismatched > 0 {
		details["mismatchedCount"] = mismatched
	}
	if unreadable > 0 {
		details["unreadableCount"] = unreadable
	}
	if invalid > 0 {
		details["invalidEntryCount"] = invalid
	}
	if len(problems) > 0 {
		details["problems"] = problems
	}

	switch {
	case mismatched > 0 || unreadable > 0 || invalid > 0:
		return CheckResult{
			ID:             "check_cas",
			Name:           "CAS Store Integrity",
			Status:         StatusFail,
			Message:        fmt.Sprintf("CAS integrity problems: %d mismatched, %d unreadable, %d invalid (of %d trees)", mismatched, unreadable, invalid, len(entries)),
			Recommendation: "Reinstall the affected listing(s); the stored content no longer matches its content address.",
			Details:        details,
		}
	case skipped > 0:
		return CheckResult{
			ID:      "check_cas",
			Name:    "CAS Store Integrity",
			Status:  StatusWarn,
			Message: fmt.Sprintf("re-verified %d of %d tree digests; %d skipped by the per-run verification limit", verified, len(entries), skipped),
			Details: details,
		}
	default:
		return CheckResult{
			ID:      "check_cas",
			Name:    "CAS Store Integrity",
			Status:  StatusPass,
			Message: fmt.Sprintf("re-verified canonical Merkle digest of %d content tree(s)", verified),
			Details: details,
		}
	}
}

// isLowerHex reports whether s has exactly n lowercase hexadecimal characters.
func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (e *Engine) checkStagingHygiene() CheckResult {
	if e.paths == nil {
		return CheckResult{ID: "check_staging", Name: "Staging Scratch Hygiene", Status: "fail"}
	}
	stagingPath := e.paths.StagingPath()
	entries, err := os.ReadDir(stagingPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{
				ID:      "check_staging",
				Name:    "Staging Scratch Hygiene",
				Status:  StatusWarn,
				Message: "staging directory not present; nothing to inspect",
				Details: map[string]any{"readError": err.Error()},
			}
		}
		return CheckResult{
			ID:             "check_staging",
			Name:           "Staging Scratch Hygiene",
			Status:         StatusFail,
			Message:        fmt.Sprintf("failed to read staging directory %s: %v", stagingPath, err),
			Recommendation: "Check filesystem permissions on DATA_ROOT/staging.",
			Details:        map[string]any{"readError": err.Error()},
		}
	}

	if len(entries) > 0 {
		return CheckResult{
			ID:             "check_staging",
			Name:           "Staging Scratch Hygiene",
			Status:         StatusWarn,
			Message:        fmt.Sprintf("%d leftover staging files found", len(entries)),
			Recommendation: "Run 'litespm doctor --repair' to clean up orphaned staging scratch files.",
			Details: map[string]any{
				"danglingFileCount": len(entries),
			},
		}
	}

	return CheckResult{
		ID:      "check_staging",
		Name:    "Staging Scratch Hygiene",
		Status:  StatusPass,
		Message: "staging scratch area clean (0 leftover files)",
	}
}

func (e *Engine) checkSecretVault(ctx context.Context) (result CheckResult) {
	if e.secretStore == nil {
		return CheckResult{
			ID:             "check_secrets",
			Name:           "OS Secret Store & Keyring",
			Status:         StatusWarn,
			Message:        "secret store is not configured; no canary was written and none can remain stored",
			Recommendation: "Initialize OS secret vault for secure OAuth and API token storage.",
		}
	}

	// Test with a synthetic canary: full put → read-back → delete round-trip.
	// The canary is fixed at 'canary'/'test_key', so cleanup is possible even
	// when the write itself failed (a store may keep a partial entry). The
	// deferred Delete below runs on every path after the write attempt, and
	// every reported outcome states whether the canary may remain stored.
	// Canary values are never echoed into messages.
	canaryValue := []byte("canary_val")
	testRef, putErr := e.secretStore.Put(ctx, "canary", "test_key", canaryValue)
	canaryRef := secrets.NewSecretRef("canary", "test_key")
	if testRef != nil {
		canaryRef = testRef
	}

	defer func() {
		if err := e.secretStore.Delete(ctx, *canaryRef); err != nil {
			if domain.ErrorCode(err) == "LPSM-STATE-NOT-FOUND" {
				result.Message = fmt.Sprintf("%s; cleanup (delete) reported no stored canary, so the canary is not stored", result.Message)
				return
			}
			result.Message = fmt.Sprintf("%s; cleanup (delete) failed: %v — the canary may remain stored as %s", result.Message, err, canaryRef.URI)
			if result.Recommendation != "" {
				result.Recommendation += " "
			}
			result.Recommendation += "Remove the canary secret ('canary'/'test_key') manually from the secret store."
			if result.Status == StatusPass {
				result.Status = StatusWarn
			}
			return
		}
		result.Message = fmt.Sprintf("%s; cleanup (delete) succeeded, the canary is not stored", result.Message)
	}()

	if putErr != nil {
		return CheckResult{
			ID:             "check_secrets",
			Name:           "OS Secret Store & Keyring",
			Status:         StatusFail,
			Message:        fmt.Sprintf("secret vault access error: %v", putErr),
			Recommendation: "Verify OS keyring daemon or credential manager permissions.",
		}
	}

	readBack, err := e.secretStore.Get(ctx, *canaryRef)
	if err != nil {
		return CheckResult{
			ID:             "check_secrets",
			Name:           "OS Secret Store & Keyring",
			Status:         StatusFail,
			Message:        fmt.Sprintf("canary write succeeded but read-back failed: %v", err),
			Recommendation: "Verify OS keyring daemon or credential manager permissions.",
		}
	}
	if !bytes.Equal(readBack, canaryValue) {
		return CheckResult{
			ID:             "check_secrets",
			Name:           "OS Secret Store & Keyring",
			Status:         StatusFail,
			Message:        "canary read-back returned a different value than was written",
			Recommendation: "The secret store is returning corrupted data; do not store credentials until this is resolved.",
		}
	}

	return CheckResult{
		ID:      "check_secrets",
		Name:    "OS Secret Store & Keyring",
		Status:  StatusPass,
		Message: "canary round-trip succeeded (put and read-back worked)",
	}
}

func (e *Engine) checkHostRegistrations(ctx context.Context) CheckResult {
	verifications, err := host.DetectInstalledHosts(ctx)
	if err != nil {
		return CheckResult{
			ID:      "check_hosts",
			Name:    "Agent Host Registrations",
			Status:  StatusWarn,
			Message: fmt.Sprintf("host detection error: %v", err),
		}
	}

	readyCount := 0
	for _, v := range verifications {
		if v.Registered {
			readyCount++
		}
	}

	msg := fmt.Sprintf("%d registered agent hosts detected", readyCount)
	if readyCount == 0 {
		// Nothing is connected yet. That is a state to fix, not a state to
		// congratulate: an all-green report that says "0 hosts" would tell the
		// reader nothing is wrong when the product is not set up at all.
		return CheckResult{
			ID:             "check_hosts",
			Name:           "Agent Host Registrations",
			Status:         StatusWarn,
			Message:        "no host configurations registered yet (run `litespm` to connect an agent)",
			Details:        map[string]any{"totalDetected": len(verifications), "readyCount": 0},
			Recommendation: "Run `litespm` and pick an agent to register the bridge entry.",
		}
	}

	return CheckResult{
		ID:      "check_hosts",
		Name:    "Agent Host Registrations",
		Status:  StatusPass,
		Message: msg,
		Details: map[string]any{
			"totalDetected": len(verifications),
			"readyCount":    readyCount,
		},
	}
}

func (e *Engine) checkHostBackups() CheckResult {
	if e.paths == nil {
		return CheckResult{
			ID:      "check_backups",
			Name:    "Host Configuration Backups",
			Status:  StatusWarn,
			Message: "platform paths uninitialized; backups were not inspected",
		}
	}
	backupsPath := e.paths.BackupsPath()
	entries, err := os.ReadDir(backupsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{
				ID:      "check_backups",
				Name:    "Host Configuration Backups",
				Status:  StatusWarn,
				Message: "backup directory not present yet (created when a host config is first modified)",
			}
		}
		return CheckResult{
			ID:             "check_backups",
			Name:           "Host Configuration Backups",
			Status:         StatusWarn,
			Message:        fmt.Sprintf("failed to read backup directory %s: %v", backupsPath, err),
			Recommendation: "Check filesystem permissions on DATA_ROOT/backups.",
		}
	}
	return CheckResult{
		ID:      "check_backups",
		Name:    "Host Configuration Backups",
		Status:  StatusPass,
		Message: fmt.Sprintf("%d configuration backup(s) found in %s", len(entries), backupsPath),
		Details: map[string]any{"backupCount": len(entries)},
	}
}

func (e *Engine) checkRuntimes() CheckResult {
	var runtimesFound []string

	if _, err := exec.LookPath("node"); err == nil {
		runtimesFound = append(runtimesFound, "Node.js")
	}
	if _, err := exec.LookPath("python3"); err == nil {
		runtimesFound = append(runtimesFound, "Python 3")
	} else if _, err := exec.LookPath("python"); err == nil {
		runtimesFound = append(runtimesFound, "Python")
	}
	if _, err := exec.LookPath("uvx"); err == nil {
		runtimesFound = append(runtimesFound, "uvx")
	}
	if _, err := exec.LookPath("npx"); err == nil {
		runtimesFound = append(runtimesFound, "npx")
	}

	if len(runtimesFound) == 0 {
		return CheckResult{
			ID:             "check_runtimes",
			Name:           "Provider Runtimes Environment",
			Status:         StatusWarn,
			Message:        "no standard scripting runtimes detected in PATH (Node.js npx, Python, uvx)",
			Details:        map[string]any{"runtimes": runtimesFound},
			Recommendation: "Install Node.js and/or Python if you intend to run MCP servers that need them; binary providers are unaffected.",
		}
	}

	return CheckResult{
		ID:      "check_runtimes",
		Name:    "Provider Runtimes Environment",
		Status:  StatusPass,
		Message: fmt.Sprintf("detected: %s", strings.Join(runtimesFound, ", ")),
		Details: map[string]any{
			"runtimes": runtimesFound,
		},
	}
}

// Free-space thresholds for the disk availability check.
const (
	diskWarnBelowBytes = 1 << 30 // 1 GiB
	diskFailBelowBytes = 100 << 20
)

func (e *Engine) checkDiskSpace() CheckResult {
	if e.paths == nil {
		return CheckResult{
			ID:      "check_disk",
			Name:    "Local Storage Disk Space",
			Status:  StatusWarn,
			Message: "platform paths uninitialized; free space was not checked",
		}
	}

	freeBytes, err := queryFreeBytes(e.paths.DataRoot)
	if err != nil {
		return CheckResult{
			ID:      "check_disk",
			Name:    "Local Storage Disk Space",
			Status:  StatusWarn,
			Message: fmt.Sprintf("free space query failed for %s: %v", e.paths.DataRoot, err),
		}
	}

	details := map[string]any{"freeBytes": freeBytes, "path": e.paths.DataRoot}
	switch {
	case freeBytes < diskFailBelowBytes:
		return CheckResult{
			ID:             "check_disk",
			Name:           "Local Storage Disk Space",
			Status:         StatusFail,
			Message:        fmt.Sprintf("only %s free in %s (below the %s minimum)", formatBytes(freeBytes), e.paths.DataRoot, formatBytes(diskFailBelowBytes)),
			Recommendation: "Free up disk space before installing; installs will fail on a full disk.",
			Details:        details,
		}
	case freeBytes < diskWarnBelowBytes:
		return CheckResult{
			ID:             "check_disk",
			Name:           "Local Storage Disk Space",
			Status:         StatusWarn,
			Message:        fmt.Sprintf("%s free in %s (below the %s comfort threshold)", formatBytes(freeBytes), e.paths.DataRoot, formatBytes(diskWarnBelowBytes)),
			Recommendation: "Consider freeing disk space; large catalogs and CAS trees need room to stage.",
			Details:        details,
		}
	default:
		return CheckResult{
			ID:      "check_disk",
			Name:    "Local Storage Disk Space",
			Status:  StatusPass,
			Message: fmt.Sprintf("%s free in %s", formatBytes(freeBytes), e.paths.DataRoot),
			Details: details,
		}
	}
}

// formatBytes renders a byte count as a human-readable binary size.
func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
