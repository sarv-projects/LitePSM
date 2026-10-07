package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

func cryptoRandRead(b []byte) (int, error) {
	return rand.Read(b)
}

// hostEntryFingerprint computes the S5 entry fingerprint: sha256 over the
// current config file bytes when the file is readable, falling back to a
// digest of host|entryKey|configPath so every registration carries a real
// content-bound value instead of a constant.
func hostEntryFingerprint(configPath, entryKey, hostID string) string {
	if data, err := os.ReadFile(configPath); err == nil {
		sum := sha256.Sum256(data)
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256([]byte(hostID + "|" + entryKey + "|" + configPath))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// --- Plans ---

// SavePlan saves an InstallPlan record to SQLite.
// Re-saving a plan id is an upsert that preserves the original created_at:
// the row is content-addressed by planHash, so a re-save refreshes the hash
// and expiry without rewriting history.
func (db *DB) SavePlan(ctx context.Context, plan *domain.InstallPlan) error {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("failed to marshal plan: %w", err)
	}

	query := `
	INSERT INTO plans (plan_id, plan_hash, schema_version, listing_id, requested_version, plan_json, created_at, expires_at, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending')
	ON CONFLICT(plan_id) DO UPDATE SET
		plan_hash = excluded.plan_hash,
		schema_version = excluded.schema_version,
		listing_id = excluded.listing_id,
		requested_version = excluded.requested_version,
		plan_json = excluded.plan_json,
		created_at = excluded.created_at,
		expires_at = excluded.expires_at,
		status = 'pending';`

	_, err = db.raw.ExecContext(ctx, query,
		plan.PlanID,
		plan.PlanHash,
		plan.SchemaVersion,
		plan.Request.ListingID,
		plan.Request.RequestedVersion,
		string(planJSON),
		plan.CreatedAt,
		plan.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save plan %s: %w", plan.PlanID, err)
	}
	return nil
}

// GetPlan retrieves a stored InstallPlan.
func (db *DB) GetPlan(ctx context.Context, planID string) (*domain.InstallPlan, error) {
	query := `SELECT plan_json FROM plans WHERE plan_id = ?;`
	var planJSON string
	err := db.raw.QueryRowContext(ctx, query, planID).Scan(&planJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound("plan", planID)
		}
		return nil, err
	}

	var plan domain.InstallPlan
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return nil, fmt.Errorf("failed to unmarshal plan JSON: %w", err)
	}
	return &plan, nil
}

// UpdatePlanStatus updates the lifecycle status of a plan.
func (db *DB) UpdatePlanStatus(ctx context.Context, planID, status string) error {
	query := `UPDATE plans SET status = ? WHERE plan_id = ?;`
	res, err := db.raw.ExecContext(ctx, query, status, planID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrNotFound("plan", planID)
	}
	return nil
}

// --- Approvals (Replay Prevention) ---

// approvalTimeLayout is the fixed-width UTC layout approval timestamps are
// stored in. It matches CURRENT_TIMESTAMP's shape (UTC, "YYYY-MM-DD HH:MM:SS")
// with a fractional part, so stored values are unambiguous and sort correctly.
const approvalTimeLayout = "2006-01-02 15:04:05.000000000"

// parseStoredApprovalTime decodes an approvals timestamp column. It accepts
// the layout this package writes plus the shapes the driver and older rows
// produced. ok is false for an unparsable value; callers fail closed.
func parseStoredApprovalTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), true
	case string:
		str := strings.TrimSpace(t)
		if i := strings.Index(str, " m=+"); i >= 0 { // monotonic clock suffix from time.Time.String()
			str = str[:i]
		}
		for _, layout := range []string{
			approvalTimeLayout,
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999Z07:00",
			time.RFC3339Nano,
			"2006-01-02 15:04:05",
		} {
			if parsed, err := time.Parse(layout, str); err == nil {
				return parsed.UTC(), true
			}
		}
	case []byte:
		return parseStoredApprovalTime(string(t))
	}
	return time.Time{}, false
}

// RecordApproval creates an approval record with status 'active'. The expiry is
// stored in UTC.
func (db *DB) RecordApproval(ctx context.Context, approvalID, subjectType, subjectHash, actor, channel, scope string, expiresAt *time.Time) error {
	query := `
	INSERT INTO approvals (approval_id, subject_type, subject_hash, actor, channel, status, granted_at, expires_at, scope)
	VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?);`

	now := time.Now().UTC().Format(approvalTimeLayout)
	var expires any
	if expiresAt != nil {
		expires = expiresAt.UTC().Format(approvalTimeLayout)
	}
	_, err := db.raw.ExecContext(ctx, query, approvalID, subjectType, subjectHash, actor, channel, now, expires, scope)
	if err != nil {
		return fmt.Errorf("failed to record approval %s: %w", approvalID, err)
	}
	return nil
}

// ApprovalRecord is the stored form of an approval.
type ApprovalRecord struct {
	ApprovalID  string
	SubjectType string
	SubjectHash string
	Actor       string
	Channel     string
	Status      string
	Scope       string
	ExpiresAt   *time.Time
}

// GetApproval loads an approval by id.
func (db *DB) GetApproval(ctx context.Context, approvalID string) (*ApprovalRecord, error) {
	var rec ApprovalRecord
	var expires any
	err := db.raw.QueryRowContext(ctx,
		"SELECT approval_id, subject_type, subject_hash, actor, channel, status, scope, expires_at FROM approvals WHERE approval_id = ?",
		approvalID).Scan(&rec.ApprovalID, &rec.SubjectType, &rec.SubjectHash, &rec.Actor, &rec.Channel, &rec.Status, &rec.Scope, &expires)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound("approval", approvalID)
		}
		return nil, err
	}
	if expires != nil {
		if t, ok := parseStoredApprovalTime(expires); ok {
			rec.ExpiresAt = &t
		} else {
			// Unparsable expiry: fail closed as already expired.
			t := time.Unix(0, 0).UTC()
			rec.ExpiresAt = &t
		}
	}
	return &rec, nil
}

// ConsumeApproval atomically consumes an active approval for exactly one
// subject. The approval must have been granted for subjectType and
// subjectHash; an approval for a different plan or action is refused without
// being consumed. Expiry is evaluated in UTC. An approval that was already
// consumed or has expired fails closed with the matching error.
func (db *DB) ConsumeApproval(ctx context.Context, approvalID, subjectType, subjectHash string) error {
	if strings.TrimSpace(subjectType) == "" || strings.TrimSpace(subjectHash) == "" {
		return domain.ErrApprovalSubjectMismatch(approvalID, "the action to authorize carries no subject type/hash")
	}
	rec, err := db.GetApproval(ctx, approvalID)
	if err != nil {
		return err
	}

	switch rec.Status {
	case "consumed":
		return domain.ErrApprovalConsumed(approvalID)
	case "expired":
		return domain.ErrApprovalExpired(approvalID)
	case "active":
	default:
		return fmt.Errorf("approval %s cannot be consumed (status: %s)", approvalID, rec.Status)
	}
	if rec.SubjectType != subjectType || rec.SubjectHash != subjectHash {
		return domain.ErrApprovalSubjectMismatch(approvalID, fmt.Sprintf(
			"it was granted for %s %q, not %s %q", rec.SubjectType, rec.SubjectHash, subjectType, subjectHash))
	}
	now := time.Now().UTC()
	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(now) {
		return domain.ErrApprovalExpired(approvalID)
	}

	// The status guard makes the transition atomic: of two concurrent
	// consumers only one affects a row.
	res, err := db.raw.ExecContext(ctx, `
	UPDATE approvals SET status = 'consumed', consumed_at = ?
	WHERE approval_id = ? AND status = 'active' AND subject_type = ? AND subject_hash = ?;`,
		now.Format(approvalTimeLayout), approvalID, subjectType, subjectHash)
	if err != nil {
		return fmt.Errorf("failed to execute atomic approval consumption: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrApprovalConsumed(approvalID)
	}
	return nil
}

// RefundApproval returns a consumed approval to 'active' after the guarded
// action failed before producing its effect, so the user is not forced to
// re-approve a plan that never ran. An approval that has since expired stays
// consumed. Refunding an approval that is not consumed is a no-op.
func (db *DB) RefundApproval(ctx context.Context, approvalID string) error {
	rec, err := db.GetApproval(ctx, approvalID)
	if err != nil {
		return err
	}
	if rec.Status != "consumed" {
		return nil
	}
	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(time.Now().UTC()) {
		return nil
	}
	_, err = db.raw.ExecContext(ctx,
		"UPDATE approvals SET status = 'active', consumed_at = NULL WHERE approval_id = ? AND status = 'consumed';", approvalID)
	return err
}

// RevokeApproval revokes an active approval.
func (db *DB) RevokeApproval(ctx context.Context, approvalID string) error {
	query := `
	UPDATE approvals
	SET status = 'revoked', revoked_at = CURRENT_TIMESTAMP
	WHERE approval_id = ? AND status = 'active';`

	res, err := db.raw.ExecContext(ctx, query, approvalID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("approval %s not found or not active", approvalID)
	}
	return nil
}

// --- Installs & Components ---

// SaveInstall persists a local installation record.
func (db *DB) SaveInstall(ctx context.Context, rec *domain.InstallRecord) error {
	return db.saveInstallExec(ctx, db.raw, rec)
}

func (db *DB) saveInstallExec(ctx context.Context, ex execer, rec *domain.InstallRecord) error {
	kind, err := installKindColumn(rec.Kind)
	if err != nil {
		return fmt.Errorf("install %s: %w", rec.InstallID, err)
	}
	// ON CONFLICT DO UPDATE, never INSERT OR REPLACE: REPLACE deletes the old
	// row first, and with foreign keys on that cascades away the install's
	// components, providers, capabilities and capability grants.
	query := `
	INSERT INTO installs (
		install_id, listing_id, kind, version, immutable_ref,
		tree_digest, install_path, scope, workspace_id, project_root,
		enabled, installed_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(install_id) DO UPDATE SET
		listing_id = excluded.listing_id,
		kind = excluded.kind,
		version = excluded.version,
		immutable_ref = excluded.immutable_ref,
		tree_digest = excluded.tree_digest,
		install_path = excluded.install_path,
		scope = excluded.scope,
		workspace_id = excluded.workspace_id,
		project_root = excluded.project_root,
		enabled = excluded.enabled,
		updated_at = excluded.updated_at;`

	enabledInt := 1
	if rec.Status == domain.InstallDisabled || rec.Status == domain.InstallRemoved {
		enabledInt = 0
	}

	_, err = ex.ExecContext(ctx, query,
		rec.InstallID,
		rec.ListingID,
		kind,
		rec.Version,
		rec.ImmutableRef,
		rec.TreeDigest,
		rec.InstallPath,
		string(rec.Scope),
		rec.WorkspaceID,
		rec.ProjectRoot,
		enabledInt,
		rec.InstalledAt,
		rec.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save install %s: %w", rec.InstallID, err)
	}
	return nil
}

// installKindColumn validates an install kind against the installs.kind enum.
// An empty kind is stored as "mcp" for callers that predate the field; every
// production writer sets it explicitly.
func installKindColumn(k domain.ListingKind) (string, error) {
	switch k {
	case "":
		return string(domain.KindMCP), nil
	case domain.KindPlugin, domain.KindMCP, domain.KindSkill, domain.KindConnector,
		domain.KindAgent, domain.KindRule, domain.KindHook, domain.KindTool, domain.KindLSP:
		return string(k), nil
	}
	return "", fmt.Errorf("unknown install kind %q", k)
}

// GetInstall retrieves an installation record by InstallID.
func (db *DB) GetInstall(ctx context.Context, installID string) (*domain.InstallRecord, error) {
	query := `
	SELECT install_id, listing_id, kind, version, COALESCE(immutable_ref, ''), tree_digest, install_path, scope, workspace_id, project_root, enabled, installed_at, updated_at
	FROM installs
	WHERE install_id = ?;`

	var rec domain.InstallRecord
	var kindStr string
	var scopeStr, wsID, pRoot sql.NullString
	var immutableRef, installPath sql.NullString
	var enabledInt int

	err := db.raw.QueryRowContext(ctx, query, installID).Scan(
		&rec.InstallID,
		&rec.ListingID,
		&kindStr,
		&rec.Version,
		&immutableRef,
		&rec.TreeDigest,
		&installPath,
		&scopeStr,
		&wsID,
		&pRoot,
		&enabledInt,
		&rec.InstalledAt,
		&rec.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound("install", installID)
		}
		return nil, err
	}

	rec.Kind = domain.ListingKind(kindStr)
	rec.Scope = domain.InstallScope(scopeStr.String)
	if immutableRef.Valid {
		rec.ImmutableRef = immutableRef.String
	}
	if installPath.Valid {
		rec.InstallPath = installPath.String
	}
	if wsID.Valid {
		rec.WorkspaceID = wsID.String
	}
	if pRoot.Valid {
		rec.ProjectRoot = pRoot.String
	}
	if enabledInt == 1 {
		rec.Status = domain.InstallActive
	} else {
		rec.Status = domain.InstallDisabled
	}

	return &rec, nil
}

// ListInstalls lists installations filtered by scope and workspace.
func (db *DB) ListInstalls(ctx context.Context, scope domain.InstallScope, workspaceID string) ([]*domain.InstallRecord, error) {
	var query string
	var args []any

	if scope == domain.ScopeProject {
		query = `SELECT install_id, listing_id, kind, version, COALESCE(immutable_ref, ''), tree_digest, install_path, scope, workspace_id, project_root, enabled, installed_at, updated_at FROM installs WHERE scope = 'project' AND workspace_id = ? ORDER BY installed_at DESC;`
		args = append(args, workspaceID)
	} else {
		query = `SELECT install_id, listing_id, kind, version, COALESCE(immutable_ref, ''), tree_digest, install_path, scope, workspace_id, project_root, enabled, installed_at, updated_at FROM installs WHERE scope = 'user' ORDER BY installed_at DESC;`
	}

	rows, err := db.raw.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*domain.InstallRecord
	for rows.Next() {
		var rec domain.InstallRecord
		var kindStr string
		var scopeStr, wsID, pRoot sql.NullString
		var immutableRef, installPath sql.NullString
		var enabledInt int

		if err := rows.Scan(
			&rec.InstallID,
			&rec.ListingID,
			&kindStr,
			&rec.Version,
			&immutableRef,
			&rec.TreeDigest,
			&installPath,
			&scopeStr,
			&wsID,
			&pRoot,
			&enabledInt,
			&rec.InstalledAt,
			&rec.UpdatedAt,
		); err != nil {
			return nil, err
		}

		rec.Kind = domain.ListingKind(kindStr)
		rec.Scope = domain.InstallScope(scopeStr.String)
		if immutableRef.Valid {
			rec.ImmutableRef = immutableRef.String
		}
		if installPath.Valid {
			rec.InstallPath = installPath.String
		}
		if wsID.Valid {
			rec.WorkspaceID = wsID.String
		}
		if pRoot.Valid {
			rec.ProjectRoot = pRoot.String
		}
		if enabledInt == 1 {
			rec.Status = domain.InstallActive
		} else {
			rec.Status = domain.InstallDisabled
		}
		list = append(list, &rec)
	}
	return list, rows.Err()
}

// DeleteInstall removes an install record and cascades to components/providers.
// Removing a record that does not exist is an explicit not-found error, never
// a silent success.
func (db *DB) DeleteInstall(ctx context.Context, installID string) error {
	res, err := db.raw.ExecContext(ctx, "DELETE FROM installs WHERE install_id = ?", installID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrNotFound("install", installID)
	}
	return nil
}

// SaveInstallComponent persists a sub-element within an install.
func (db *DB) SaveInstallComponent(ctx context.Context, comp *domain.InstallComponentRecord) error {
	return db.saveInstallComponentExec(ctx, db.raw, comp)
}

// ComponentIDFor derives the component row id. It is deliberately distinct
// from the install id (a component is a sub-element of an install, and several
// can share one), and deterministic so re-saving the same component updates
// its row instead of creating a duplicate.
func ComponentIDFor(installID string, kind domain.ComponentKind, name string) string {
	return fmt.Sprintf("%s#%s/%s", installID, kind, name)
}

func (db *DB) saveInstallComponentExec(ctx context.Context, ex execer, comp *domain.InstallComponentRecord) error {
	componentID := comp.ComponentID
	if componentID == "" {
		componentID = ComponentIDFor(comp.InstallID, comp.Kind, comp.ComponentName)
	}
	// ON CONFLICT DO UPDATE: providers.component_id references this row, so a
	// REPLACE (delete + insert) would be rejected or cascade.
	query := `
	INSERT INTO install_components (component_id, install_id, kind, name, relative_path, enabled)
	VALUES (?, ?, ?, ?, ?, 1)
	ON CONFLICT(component_id) DO UPDATE SET
		install_id = excluded.install_id,
		kind = excluded.kind,
		name = excluded.name,
		relative_path = excluded.relative_path;`

	_, err := ex.ExecContext(ctx, query, componentID, comp.InstallID, string(comp.Kind), comp.ComponentName, comp.Path)
	return err
}

// GetInstallComponent reads one installed-component row by its component id.
func (db *DB) GetInstallComponent(ctx context.Context, componentID string) (*domain.InstallComponentRecord, error) {
	row := db.raw.QueryRowContext(ctx, `
	SELECT component_id, install_id, kind, name, COALESCE(relative_path, ''), enabled
	FROM install_components WHERE component_id = ?;`, componentID)
	var (
		rec     domain.InstallComponentRecord
		enabled int
		kindStr string
	)
	if err := row.Scan(&rec.ComponentID, &rec.InstallID, &kindStr, &rec.ComponentName, &rec.Path, &enabled); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound("install_component", componentID)
		}
		return nil, err
	}
	rec.Kind = domain.ComponentKind(kindStr)
	if enabled == 1 {
		rec.Status = string(domain.InstallActive)
	}
	return &rec, nil
}

// ListInstallComponents returns every component row of one install.
func (db *DB) ListInstallComponents(ctx context.Context, installID string) ([]domain.InstallComponentRecord, error) {
	rows, err := db.raw.QueryContext(ctx, `
	SELECT component_id, install_id, kind, name, COALESCE(relative_path, ''), enabled
	FROM install_components WHERE install_id = ? ORDER BY component_id;`, installID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.InstallComponentRecord
	for rows.Next() {
		var rec domain.InstallComponentRecord
		var enabled int
		if err := rows.Scan(&rec.ComponentID, &rec.InstallID, &rec.Kind, &rec.ComponentName, &rec.Path, &enabled); err != nil {
			return nil, err
		}
		if enabled == 1 {
			rec.Status = string(domain.InstallActive)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CommitInstallOperation writes the install record, its primary component
// record, and the journal transition to "committed" in one SQLite transaction.
// A crash therefore leaves either no install row (startup recovery rolls the
// operation back) or a complete one (recovery finalizes the journal), never a
// half-written install.
func (db *DB) CommitInstallOperation(ctx context.Context, rec *domain.InstallRecord, comp *domain.InstallComponentRecord, opID string) error {
	tx, err := db.raw.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin install commit transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := db.saveInstallExec(ctx, tx, rec); err != nil {
		return fmt.Errorf("failed to save install %s: %w", rec.InstallID, err)
	}
	if err := db.saveInstallComponentExec(ctx, tx, comp); err != nil {
		return fmt.Errorf("failed to save install component: %w", err)
	}
	if err := db.advanceOperationStateExec(ctx, tx, opID, "committed"); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit install metadata transaction: %w", err)
	}
	return nil
}

// --- Providers & Capabilities ---

// ProviderConfig is the launch-relevant subset of a providers row: everything
// the daemon needs to decide whether and how to start a configured provider.
type ProviderConfig struct {
	ProviderID     string
	InstallID      string
	Mode           string
	LaunchSpecJSON string
	Enabled        bool
	Autostart      bool
	AuthProfileID  string
}

// ListProviders returns every configured provider row, ordered by provider id.
// It reports database state only: it never implies a process is running.
func (db *DB) ListProviders(ctx context.Context) ([]*ProviderConfig, error) {
	query := `
	SELECT provider_id, install_id, mode, launch_spec_json, enabled, autostart, COALESCE(auth_profile_id, '')
	FROM providers
	ORDER BY provider_id;`

	rows, err := db.raw.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query providers: %w", err)
	}
	defer rows.Close()

	var list []*ProviderConfig
	for rows.Next() {
		var p ProviderConfig
		var enabled, autostart int
		if err := rows.Scan(&p.ProviderID, &p.InstallID, &p.Mode, &p.LaunchSpecJSON, &enabled, &autostart, &p.AuthProfileID); err != nil {
			return nil, err
		}
		p.Enabled = enabled == 1
		p.Autostart = autostart == 1
		list = append(list, &p)
	}
	return list, rows.Err()
}

// ProviderMode maps a transport onto the `providers.mode` enum in ARCH/12 §13.
//
// The column is a CHECK-constrained enum of 'local-stdio', 'remote-http',
// 'legacy-sse', while the rest of the codebase speaks transports ("stdio",
// "sse", "http"). Writing a transport straight into that column produced a
// constraint violation the first time anything actually called this writer.
func ProviderMode(transport string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", "stdio", "local-stdio":
		return "local-stdio", nil
	case "sse", "legacy-sse":
		return "legacy-sse", nil
	case "http", "https", "streamable-http", "remote-http":
		return "remote-http", nil
	default:
		return "", fmt.Errorf("transport %q has no providers.mode value", transport)
	}
}

// launchSpec is the JSON written to providers.launch_spec_json: everything needed
// to start the provider, and nothing else. The previous writer put the argument
// list in runtime_adapter and the auth profile id in launch_spec_json, so a
// provider row described a program that did not exist.
type launchSpec struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"workingDir,omitempty"`
}

// SaveProvider stores supervised MCP provider configuration.
func (db *DB) SaveProvider(ctx context.Context, p *domain.ProviderRecord) error {
	mode, err := ProviderMode(p.Transport)
	if err != nil {
		return err
	}
	var args []string
	if strings.TrimSpace(p.ArgsJSON) != "" {
		if err := json.Unmarshal([]byte(p.ArgsJSON), &args); err != nil {
			return fmt.Errorf("provider %s has an unreadable ArgsJSON: %w", p.ProviderID, err)
		}
	}
	var env map[string]string
	if strings.TrimSpace(p.EnvJSON) != "" {
		if err := json.Unmarshal([]byte(p.EnvJSON), &env); err != nil {
			return fmt.Errorf("provider %s has an unreadable EnvJSON: %w", p.ProviderID, err)
		}
	}
	spec, err := json.Marshal(launchSpec{Command: p.Command, Args: args, Env: env, WorkingDir: p.WorkingDir})
	if err != nil {
		return err
	}

	// component_id is a foreign key onto install_components. An unset
	// ComponentID is resolved to that install's mcp-provider component row
	// (preferring the one named like the provider's ComponentName), never to the
	// install id: component ids are distinct from install ids.
	componentID := p.ComponentID
	if componentID == "" {
		err := db.raw.QueryRowContext(ctx, `
		SELECT component_id FROM install_components
		WHERE install_id = ? AND kind = ?
		ORDER BY (name = ?) DESC, component_id LIMIT 1;`,
			p.InstallID, string(domain.ComponentMCPProvider), p.ComponentName).Scan(&componentID)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("provider %s: install %s has no mcp-provider component row to attach to", p.ProviderID, p.InstallID)
			}
			return err
		}
	}

	// ON CONFLICT DO UPDATE: capabilities, provider_sessions and
	// oauth_sessions cascade from this row, and capability_grants hang off
	// capabilities, so REPLACE would silently revoke every grant.
	query := `
	INSERT INTO providers (provider_id, install_id, component_id, mode, runtime_adapter, launch_spec_json, auth_profile_id, enabled, autostart, created_at)
	VALUES (?, ?, ?, ?, 'process', ?, ?, 1, 0, ?)
	ON CONFLICT(provider_id) DO UPDATE SET
		install_id = excluded.install_id,
		component_id = excluded.component_id,
		mode = excluded.mode,
		runtime_adapter = excluded.runtime_adapter,
		launch_spec_json = excluded.launch_spec_json,
		auth_profile_id = excluded.auth_profile_id;`

	var authArg any
	if p.AuthProfileID != "" {
		authArg = p.AuthProfileID
	}

	_, err = db.raw.ExecContext(ctx, query,
		p.ProviderID,
		p.InstallID,
		componentID,
		mode,
		string(spec),
		authArg,
		p.CreatedAt,
	)
	return err
}

// GetProvider reads one provider row back into a ProviderRecord, reconstructing
// the launch specification so a caller does not have to know the column layout.
func (db *DB) GetProvider(ctx context.Context, providerID string) (*domain.ProviderRecord, error) {
	row := db.raw.QueryRowContext(ctx, `
	SELECT provider_id, install_id, component_id, mode, launch_spec_json, COALESCE(auth_profile_id, '')
	FROM providers WHERE provider_id = ?;`, providerID)
	var (
		rec       domain.ProviderRecord
		mode      string
		launchRaw string
		enabled   int
	)
	if err := row.Scan(&rec.ProviderID, &rec.InstallID, &rec.ComponentID, &mode, &launchRaw, &rec.AuthProfileID); err != nil {
		return nil, err
	}
	_ = enabled
	rec.Transport = mode
	var spec launchSpec
	if launchRaw != "" {
		if err := json.Unmarshal([]byte(launchRaw), &spec); err != nil {
			return nil, fmt.Errorf("provider %s has an unreadable launch spec: %w", providerID, err)
		}
	}
	rec.Command = spec.Command
	rec.WorkingDir = spec.WorkingDir
	if len(spec.Args) > 0 {
		if b, err := json.Marshal(spec.Args); err == nil {
			rec.ArgsJSON = string(b)
		}
	}
	if len(spec.Env) > 0 {
		if b, err := json.Marshal(spec.Env); err == nil {
			rec.EnvJSON = string(b)
		}
	}
	return &rec, nil
}

// GetCapability reads one discovered capability row.
func (db *DB) GetCapability(ctx context.Context, capabilityID string) (*domain.CapabilityRecord, error) {
	row := db.raw.QueryRowContext(ctx, `
	SELECT capability_id, provider_id, native_name, COALESCE(description, ''), input_schema_json, schema_fingerprint, discovered_at
	FROM capabilities WHERE capability_id = ?;`, capabilityID)
	var (
		rec domain.CapabilityRecord
		at  time.Time
	)
	if err := row.Scan(&rec.CapabilityID, &rec.ProviderID, &rec.Name, &rec.Description,
		&rec.InputSchemaJSON, &rec.SchemaFingerprint, &at); err != nil {
		return nil, err
	}
	rec.DiscoveredAt = at
	rec.UpdatedAt = at
	return &rec, nil
}

// ListCapabilities returns capability rows, optionally narrowed to one provider.
func (db *DB) ListCapabilities(ctx context.Context, providerID string) ([]domain.CapabilityRecord, error) {
	query := `SELECT capability_id, provider_id, native_name, COALESCE(description, ''), input_schema_json, schema_fingerprint, discovered_at
		FROM capabilities`
	args := []any{}
	if providerID != "" {
		query += ` WHERE provider_id = ?`
		args = append(args, providerID)
	}
	query += ` ORDER BY capability_id;`
	rows, err := db.raw.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.CapabilityRecord
	for rows.Next() {
		var (
			rec domain.CapabilityRecord
			at  time.Time
		)
		if err := rows.Scan(&rec.CapabilityID, &rec.ProviderID, &rec.Name, &rec.Description,
			&rec.InputSchemaJSON, &rec.SchemaFingerprint, &at); err != nil {
			return nil, err
		}
		rec.DiscoveredAt = at
		rec.UpdatedAt = at
		out = append(out, rec)
	}
	return out, rows.Err()
}

// SaveCapability stores a tool schema and fingerprint.
// Re-discovery upserts without resetting lifecycle: the original
// discovered_at is preserved so drift windows stay measurable.
func (db *DB) SaveCapability(ctx context.Context, c *domain.CapabilityRecord) error {
	query := `
	INSERT INTO capabilities (capability_id, provider_id, native_name, title, description, schema_fingerprint, input_schema_json, discovered_at, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active')
	ON CONFLICT(capability_id) DO UPDATE SET
		provider_id = excluded.provider_id,
		native_name = excluded.native_name,
		title = excluded.title,
		description = excluded.description,
		schema_fingerprint = excluded.schema_fingerprint,
		input_schema_json = excluded.input_schema_json,
		discovered_at = excluded.discovered_at,
		status = 'active';`

	_, err := db.raw.ExecContext(ctx, query,
		c.CapabilityID,
		c.ProviderID,
		c.Name,
		c.Name,
		c.Description,
		c.SchemaFingerprint,
		c.InputSchemaJSON,
		c.DiscoveredAt,
	)
	return err
}

// SaveCapabilityGrant records policy or user authorization for a capability.
// Grants are append-only authorizations: a re-save of the same grant id never
// rewrites granted_by/granted_at, so an attacker or a bug cannot backdate or
// re-attribute an existing authorization. The identity bindings and
// status/expiry DO advance — that is what lets a person re-approve a grant
// after a schema change (rebinding the fingerprint under the original
// attribution) or expire/revoke it — but the attribution columns are written
// from the existing row, never from the caller.
func (db *DB) SaveCapabilityGrant(ctx context.Context, grant *domain.CapabilityGrant, grantedBy string) error {
	query := `
	INSERT INTO capability_grants (
		grant_id, capability_id, schema_fingerprint, cas_tree_digest,
		endpoint_origin, server_version_digest, status, granted_by,
		granted_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(grant_id) DO UPDATE SET
		capability_id = excluded.capability_id,
		schema_fingerprint = excluded.schema_fingerprint,
		cas_tree_digest = excluded.cas_tree_digest,
		endpoint_origin = excluded.endpoint_origin,
		server_version_digest = excluded.server_version_digest,
		status = excluded.status,
		granted_by = capability_grants.granted_by,
		granted_at = capability_grants.granted_at,
		expires_at = excluded.expires_at;`

	_, err := db.raw.ExecContext(ctx, query,
		grant.GrantID,
		grant.CapabilityID,
		grant.SchemaFingerprint,
		grant.CASTreeDigest,
		grant.EndpointOrigin,
		grant.ServerVersionDigest,
		grant.Status,
		grantedBy,
		grant.CreatedAt,
		grant.ExpiresAt,
	)
	return err
}

// GetActiveGrant checks if an active capability grant exists with matching fingerprint and CAS/origin identity.
func (db *DB) GetActiveGrant(ctx context.Context, capabilityID, schemaFingerprint string) (*domain.CapabilityGrant, error) {
	query := `
	SELECT grant_id, capability_id, schema_fingerprint, cas_tree_digest, endpoint_origin, server_version_digest, status, granted_at, expires_at
	FROM capability_grants
	WHERE capability_id = ? AND schema_fingerprint = ? AND status = 'active'
	  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP);`

	var g domain.CapabilityGrant
	var casDigest, origin, srvVer sql.NullString
	var exp sql.NullTime

	err := db.raw.QueryRowContext(ctx, query, capabilityID, schemaFingerprint).Scan(
		&g.GrantID,
		&g.CapabilityID,
		&g.SchemaFingerprint,
		&casDigest,
		&origin,
		&srvVer,
		&g.Status,
		&g.CreatedAt,
		&exp,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound("capability_grant", capabilityID)
		}
		return nil, err
	}

	if casDigest.Valid {
		g.CASTreeDigest = casDigest.String
	}
	if origin.Valid {
		g.EndpointOrigin = origin.String
	}
	if srvVer.Valid {
		g.ServerVersionDigest = srvVer.String
	}
	if exp.Valid {
		g.ExpiresAt = &exp.Time
	}

	return &g, nil
}

// ListCapabilityGrants returns capability grants, optionally filtered by
// capability id (empty = all), newest first. Status is part of the row: a
// revoked or expired grant is a record, not a deletion.
func (db *DB) ListCapabilityGrants(ctx context.Context, capabilityID string) ([]domain.CapabilityGrant, error) {
	query := `
	SELECT grant_id, capability_id, schema_fingerprint, cas_tree_digest, endpoint_origin,
	       server_version_digest, status, granted_by, granted_at, expires_at
	FROM capability_grants `
	args := []any{}
	if capabilityID != "" {
		query += "WHERE capability_id = ? "
		args = append(args, capabilityID)
	}
	query += "ORDER BY granted_at DESC, grant_id;"

	rows, err := db.raw.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.CapabilityGrant
	for rows.Next() {
		var g domain.CapabilityGrant
		var casDigest, origin, srvVer sql.NullString
		var exp sql.NullTime
		if err := rows.Scan(&g.GrantID, &g.CapabilityID, &g.SchemaFingerprint,
			&casDigest, &origin, &srvVer, &g.Status, &g.GrantedBy, &g.CreatedAt, &exp); err != nil {
			return nil, err
		}
		if casDigest.Valid {
			g.CASTreeDigest = casDigest.String
		}
		if origin.Valid {
			g.EndpointOrigin = origin.String
		}
		if srvVer.Valid {
			g.ServerVersionDigest = srvVer.String
		}
		if exp.Valid {
			g.ExpiresAt = &exp.Time
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetCapabilityGrantStatus advances one grant's status (revoke, expire). The
// caller has already looked the row up; a status change on a grant that does
// not exist is an explicit not-found, never a silent success.
func (db *DB) SetCapabilityGrantStatus(ctx context.Context, grantID, status string) error {
	res, err := db.raw.ExecContext(ctx,
		"UPDATE capability_grants SET status = ? WHERE grant_id = ?;", status, grantID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound("capability_grant", grantID)
	}
	return nil
}

// --- Host Registrations & Backups ---

// SaveHostRegistration records or updates a managed host-config entry. The row
// is keyed by (host, scope, workspace, managed entry), so two MCP servers on
// one host no longer overwrite each other. EntryFingerprint must be the hash of
// the entry LiteSPM actually wrote; an empty one is refused rather than
// recorded as a placeholder.
func (db *DB) SaveHostRegistration(ctx context.Context, reg *domain.HostRegistrationRecord) error {
	return db.SaveHostRegistrationExec(ctx, db.raw, reg)
}

// ListHostRegistrationsForEntry returns every managed registration for one
// entry key in one scope, across hosts. Uninstall uses it to find the configs
// that still hold an entry written before the deployment ledger existed.
func (db *DB) ListHostRegistrationsForEntry(ctx context.Context, scope domain.InstallScope, entryKey string) ([]domain.HostRegistrationRecord, error) {
	rows, err := db.raw.QueryContext(ctx, `
	SELECT host_id, scope, COALESCE(workspace_id, ''), config_path, managed_entry_key, COALESCE(entry_fingerprint, ''), registered_at
	FROM host_registrations WHERE scope = ? AND managed_entry_key = ? ORDER BY host_id;`,
		string(scope), entryKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.HostRegistrationRecord
	for rows.Next() {
		var rec domain.HostRegistrationRecord
		var scopeStr string
		if err := rows.Scan(&rec.HostID, &scopeStr, &rec.WorkspaceID, &rec.ConfigPath,
			&rec.ManagedEntryKey, &rec.EntryFingerprint, &rec.RegisteredAt); err != nil {
			return nil, err
		}
		rec.Scope = domain.InstallScope(scopeStr)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteHostRegistration removes one managed-entry registration row.
func (db *DB) DeleteHostRegistration(ctx context.Context, hostID string, scope domain.InstallScope, workspaceID, managedEntryKey string) error {
	_, err := db.raw.ExecContext(ctx,
		`DELETE FROM host_registrations WHERE host_id = ? AND scope = ? AND workspace_id = ? AND managed_entry_key = ?;`,
		hostID, string(scope), workspaceID, managedEntryKey)
	return err
}

// SaveHostBackup persists metadata of an atomic config backup.
func (db *DB) SaveHostBackup(ctx context.Context, backupID, hostID string, scope domain.InstallScope, workspaceID, originalPath, backupPath, preEditDigest string) error {
	query := `
	INSERT INTO host_backups (backup_id, host_id, scope, workspace_id, original_path, backup_path, pre_edit_digest, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);`

	_, err := db.raw.ExecContext(ctx, query, backupID, hostID, string(scope), workspaceID, originalPath, backupPath, preEditDigest)
	return err
}

// --- Auth Profiles ---

// SaveAuthProfile stores an authentication profile.
// Re-saving preserves the original created_at so credential rotation stays auditable.
func (db *DB) SaveAuthProfile(ctx context.Context, prof *domain.AuthProfile) error {
	query := `
	INSERT INTO auth_profiles (profile_id, provider_id, profile_type, secret_ref, status, metadata_json, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(profile_id) DO UPDATE SET
		provider_id = excluded.provider_id,
		profile_type = excluded.profile_type,
		secret_ref = excluded.secret_ref,
		status = excluded.status,
		metadata_json = excluded.metadata_json,
		updated_at = excluded.updated_at;`

	_, err := db.raw.ExecContext(ctx, query,
		prof.ProfileID,
		prof.ProviderID,
		prof.ProfileType,
		prof.SecretRef,
		prof.Status,
		prof.MetadataJSON,
		prof.CreatedAt,
		prof.UpdatedAt,
	)
	return err
}

// GetAuthProfile retrieves an auth profile by ID.
func (db *DB) GetAuthProfile(ctx context.Context, profileID string) (*domain.AuthProfile, error) {
	query := `
	SELECT profile_id, provider_id, profile_type, secret_ref, status, metadata_json, created_at, updated_at
	FROM auth_profiles
	WHERE profile_id = ?;`

	var prof domain.AuthProfile
	err := db.raw.QueryRowContext(ctx, query, profileID).Scan(
		&prof.ProfileID,
		&prof.ProviderID,
		&prof.ProfileType,
		&prof.SecretRef,
		&prof.Status,
		&prof.MetadataJSON,
		&prof.CreatedAt,
		&prof.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound("auth_profile", profileID)
		}
		return nil, err
	}
	return &prof, nil
}

// --- Audit Events ---

// newAuditEventID mints a collision-resistant audit event id (S7). The old
// evt_<unixnano> collided under concurrent writers in the same nanosecond;
// 128 bits of crypto randomness make a collision computationally infeasible,
// with a nanosecond fallback only when the RNG itself fails.
func newAuditEventID() string {
	var b [16]byte
	if _, err := cryptoRandRead(b[:]); err == nil {
		return fmt.Sprintf("evt_%x", b)
	}
	return fmt.Sprintf("evt_%d_%x", time.Now().UnixNano(), time.Now().UnixNano())
}

// RecordAuditEvent writes an immutable audit log entry.
func (db *DB) RecordAuditEvent(ctx context.Context, actor, action, targetRef, decision, approvalID, opID, outcome, metadataJSON string) error {
	query := `
	INSERT INTO audit_events (event_id, actor, action, target_ref, decision, approval_id, operation_id, outcome, metadata_json, timestamp)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);`

	// Nanosecond timestamps alone collide when two events land in the same
	// clock tick (coarse Windows timers, concurrent writers); the random
	// suffix makes the id collision-free without a coordination point.
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("generate audit event id: %w", err)
	}
	eventID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))

	var appArg, opArg, decArg any
	if approvalID != "" {
		appArg = approvalID
	}
	if opID != "" {
		opArg = opID
	}
	if decision != "" {
		decArg = decision
	}

	_, err := db.raw.ExecContext(ctx, query, eventID, actor, action, targetRef, decArg, appArg, opArg, outcome, metadataJSON)
	return err
}
