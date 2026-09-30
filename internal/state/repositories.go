package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// --- Plans ---

// SavePlan saves an InstallPlan record to SQLite.
func (db *DB) SavePlan(ctx context.Context, plan *domain.InstallPlan) error {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("failed to marshal plan: %w", err)
	}

	query := `
	INSERT OR REPLACE INTO plans (plan_id, plan_hash, schema_version, listing_id, requested_version, plan_json, created_at, expires_at, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending');`

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

// RecordApproval creates an approval record with status 'active'.
func (db *DB) RecordApproval(ctx context.Context, approvalID, subjectType, subjectHash, actor, channel, scope string, expiresAt *time.Time) error {
	query := `
	INSERT INTO approvals (approval_id, subject_type, subject_hash, actor, channel, status, granted_at, expires_at, scope)
	VALUES (?, ?, ?, ?, ?, 'active', CURRENT_TIMESTAMP, ?, ?);`

	_, err := db.raw.ExecContext(ctx, query, approvalID, subjectType, subjectHash, actor, channel, expiresAt, scope)
	if err != nil {
		return fmt.Errorf("failed to record approval %s: %w", approvalID, err)
	}
	return nil
}

// ConsumeApproval atomically consumes an active approval.
// If the approval was already consumed, it fails closed with LPSM-POLICY-APPROVAL-CONSUMED.
func (db *DB) ConsumeApproval(ctx context.Context, approvalID string) error {
	query := `
	UPDATE approvals
	SET status = 'consumed', consumed_at = CURRENT_TIMESTAMP
	WHERE approval_id = ? AND status = 'active';`

	res, err := db.raw.ExecContext(ctx, query, approvalID)
	if err != nil {
		return fmt.Errorf("failed to execute atomic approval consumption: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		// Determine why it failed: already consumed, expired, or non-existent
		var status string
		var expiresAt sql.NullTime
		checkErr := db.raw.QueryRowContext(ctx, "SELECT status, expires_at FROM approvals WHERE approval_id = ?", approvalID).Scan(&status, &expiresAt)
		if checkErr != nil {
			if checkErr == sql.ErrNoRows {
				return domain.ErrNotFound("approval", approvalID)
			}
			return checkErr
		}

		if status == "consumed" {
			return domain.ErrApprovalConsumed(approvalID)
		}
		if status == "expired" || (expiresAt.Valid && expiresAt.Time.Before(time.Now())) {
			return domain.ErrApprovalExpired(approvalID)
		}
		return fmt.Errorf("approval %s cannot be consumed (status: %s)", approvalID, status)
	}

	return nil
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
	query := `
	INSERT OR REPLACE INTO installs (
		install_id, listing_id, kind, version, immutable_ref,
		tree_digest, install_path, scope, workspace_id, project_root,
		enabled, installed_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

	enabledInt := 1
	if rec.Status == domain.InstallDisabled || rec.Status == domain.InstallRemoved {
		enabledInt = 0
	}

	_, err := db.raw.ExecContext(ctx, query,
		rec.InstallID,
		rec.ListingID,
		"mcp", // default kind
		rec.Version,
		"",
		rec.TreeDigest,
		rec.ProjectRoot,
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

// GetInstall retrieves an installation record by InstallID.
func (db *DB) GetInstall(ctx context.Context, installID string) (*domain.InstallRecord, error) {
	query := `
	SELECT install_id, listing_id, version, tree_digest, scope, workspace_id, project_root, enabled, installed_at, updated_at
	FROM installs
	WHERE install_id = ?;`

	var rec domain.InstallRecord
	var scopeStr, wsID, pRoot sql.NullString
	var enabledInt int

	err := db.raw.QueryRowContext(ctx, query, installID).Scan(
		&rec.InstallID,
		&rec.ListingID,
		&rec.Version,
		&rec.TreeDigest,
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

	rec.Scope = domain.InstallScope(scopeStr.String)
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
		query = `SELECT install_id, listing_id, version, tree_digest, scope, workspace_id, project_root, enabled, installed_at, updated_at FROM installs WHERE scope = 'project' AND workspace_id = ? ORDER BY installed_at DESC;`
		args = append(args, workspaceID)
	} else {
		query = `SELECT install_id, listing_id, version, tree_digest, scope, workspace_id, project_root, enabled, installed_at, updated_at FROM installs WHERE scope = 'user' ORDER BY installed_at DESC;`
	}

	rows, err := db.raw.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*domain.InstallRecord
	for rows.Next() {
		var rec domain.InstallRecord
		var scopeStr, wsID, pRoot sql.NullString
		var enabledInt int

		if err := rows.Scan(
			&rec.InstallID,
			&rec.ListingID,
			&rec.Version,
			&rec.TreeDigest,
			&scopeStr,
			&wsID,
			&pRoot,
			&enabledInt,
			&rec.InstalledAt,
			&rec.UpdatedAt,
		); err != nil {
			return nil, err
		}

		rec.Scope = domain.InstallScope(scopeStr.String)
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
func (db *DB) DeleteInstall(ctx context.Context, installID string) error {
	_, err := db.raw.ExecContext(ctx, "DELETE FROM installs WHERE install_id = ?", installID)
	return err
}

// SaveInstallComponent persists a sub-element within an install.
func (db *DB) SaveInstallComponent(ctx context.Context, comp *domain.InstallComponentRecord) error {
	query := `
	INSERT OR REPLACE INTO install_components (component_id, install_id, kind, name, relative_path, enabled)
	VALUES (?, ?, ?, ?, ?, 1);`

	_, err := db.raw.ExecContext(ctx, query, comp.InstallID, comp.InstallID, string(comp.Kind), comp.ComponentName, comp.Path)
	return err
}

// --- Providers & Capabilities ---

// SaveProvider stores supervised MCP provider configuration.
func (db *DB) SaveProvider(ctx context.Context, p *domain.ProviderRecord) error {
	query := `
	INSERT OR REPLACE INTO providers (provider_id, install_id, component_id, mode, runtime_adapter, launch_spec_json, auth_profile_id, enabled, autostart, created_at)
	VALUES (?, ?, ?, ?, 'process', ?, ?, 1, 0, ?);`

	var authArg any
	if p.AuthProfileID != "" {
		authArg = p.AuthProfileID
	}

	_, err := db.raw.ExecContext(ctx, query,
		p.ProviderID,
		p.InstallID,
		p.ComponentName,
		p.Transport,
		p.ArgsJSON,
		authArg,
		p.CreatedAt,
	)
	return err
}

// SaveCapability stores a tool schema and fingerprint.
func (db *DB) SaveCapability(ctx context.Context, c *domain.CapabilityRecord) error {
	query := `
	INSERT OR REPLACE INTO capabilities (capability_id, provider_id, native_name, title, description, schema_fingerprint, input_schema_json, discovered_at, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active');`

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
func (db *DB) SaveCapabilityGrant(ctx context.Context, grant *domain.CapabilityGrant, grantedBy string) error {
	query := `
	INSERT OR REPLACE INTO capability_grants (
		grant_id, capability_id, schema_fingerprint, cas_tree_digest,
		endpoint_origin, server_version_digest, status, granted_by,
		granted_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

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

// --- Host Registrations & Backups ---

// SaveHostRegistration records or updates an agent host registration.
func (db *DB) SaveHostRegistration(ctx context.Context, reg *domain.HostRegistrationRecord) error {
	query := `
	INSERT OR REPLACE INTO host_registrations (host_id, scope, workspace_id, config_path, managed_entry_key, entry_fingerprint, registered_at)
	VALUES (?, ?, ?, ?, 'litepsm', 'fp_default', ?);`

	_, err := db.raw.ExecContext(ctx, query,
		reg.HostID,
		string(reg.Scope),
		reg.WorkspaceID,
		reg.ConfigPath,
		reg.RegisteredAt,
	)
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
func (db *DB) SaveAuthProfile(ctx context.Context, prof *domain.AuthProfile) error {
	query := `
	INSERT OR REPLACE INTO auth_profiles (profile_id, provider_id, profile_type, secret_ref, status, metadata_json, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?);`

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

// RecordAuditEvent writes an immutable audit log entry.
func (db *DB) RecordAuditEvent(ctx context.Context, actor, action, targetRef, decision, approvalID, opID, outcome, metadataJSON string) error {
	query := `
	INSERT INTO audit_events (event_id, actor, action, target_ref, decision, approval_id, operation_id, outcome, metadata_json, timestamp)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);`

	eventID := fmt.Sprintf("evt_%d", time.Now().UnixNano())

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
