# Storage, Transactions & Crash Recovery

## 1. SQLite Engine Configuration

All structured local metadata is persisted in SQLite 3 (`DATA_ROOT/state.db`). The database is configured for high-concurrency non-blocking reads and crash durability:

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA encoding = 'UTF-8';
```

---

## 2. Schema DDL: The 22 Core Relational Tables

```sql
-- 1. Schema Migrations Tracker
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    description TEXT NOT NULL
);

-- 2. System Settings
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 3. Configured Catalog Sources
CREATE TABLE sources (
    source_id TEXT PRIMARY KEY, -- e.g. builtin:mcp-registry
    namespace TEXT NOT NULL,
    slug TEXT NOT NULL,
    source_type TEXT NOT NULL,  -- mcp-registry | agent-skills | claude-market | git
    source_url TEXT,
    config_json TEXT NOT NULL DEFAULT '{}',
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 4. Source Snapshots
CREATE TABLE source_snapshots (
    snapshot_id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES sources(source_id),
    adapter_version TEXT NOT NULL,
    upstream_revision TEXT,
    status TEXT NOT NULL CHECK(status IN ('healthy', 'partial', 'failed', 'stale')),
    item_count INTEGER NOT NULL DEFAULT 0,
    content_digest TEXT,
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP
);

-- 5. Cached Catalog Releases
CREATE TABLE catalog_releases (
    release_id TEXT PRIMARY KEY,
    sequence INTEGER NOT NULL UNIQUE,
    manifest_digest TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    cached_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 6. Generated Install Plans
CREATE TABLE plans (
    plan_id TEXT PRIMARY KEY,
    plan_hash TEXT NOT NULL,
    schema_version INTEGER NOT NULL DEFAULT 2,
    listing_id TEXT NOT NULL,
    requested_version TEXT,
    plan_json TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending', 'approved', 'executed', 'expired', 'rejected'))
);
CREATE INDEX idx_plans_hash ON plans(plan_hash);

-- 7. User Approvals
CREATE TABLE approvals (
    approval_id TEXT PRIMARY KEY,
    subject_type TEXT NOT NULL CHECK(subject_type IN ('install-plan', 'update-plan', 'removal-plan', 'capability-call', 'capability-grant')),
    subject_hash TEXT NOT NULL,
    actor TEXT NOT NULL,
    channel TEXT NOT NULL CHECK(channel IN ('cli-tty', 'mcp-elicitation', 'native-host', 'preconfigured-policy')),
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'consumed', 'revoked', 'expired')),
    consumed_at TIMESTAMP,
    revoked_at TIMESTAMP,
    granted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    scope TEXT NOT NULL DEFAULT 'user'
);
CREATE INDEX idx_approvals_subject ON approvals(subject_type, subject_hash);

-- 8. Operations Journal
CREATE TABLE operations (
    operation_id TEXT PRIMARY KEY,
    plan_id TEXT REFERENCES plans(plan_id),
    operation_type TEXT NOT NULL CHECK(operation_type IN ('install', 'update', 'remove', 'host_setup')),
    state TEXT NOT NULL CHECK(state IN (
        'created', 'resolving', 'awaiting_approval', 'approved',
        'fetching', 'verified', 'staging', 'commit_intent',
        'committing', 'committed', 'rolling_back', 'rolled_back',
        'failed', 'cancelled'
    )),
    idempotency_key TEXT UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 9. Operation Journal Steps (Audit Log for Recovery)
CREATE TABLE operation_steps (
    step_id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation_id TEXT NOT NULL REFERENCES operations(operation_id),
    step_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending', 'running', 'done', 'failed')),
    detail TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 10. Downloaded Artifacts (CAS Metadata)
CREATE TABLE artifacts (
    artifact_id TEXT PRIMARY KEY,
    digest TEXT NOT NULL UNIQUE, -- sha256:<hex>
    artifact_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    local_path TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 11. Local Installations
CREATE TABLE installs (
    install_id TEXT PRIMARY KEY,
    listing_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('plugin', 'mcp', 'skill', 'connector')),
    version TEXT NOT NULL,
    immutable_ref TEXT,
    tree_digest TEXT NOT NULL, -- CAS tree digest
    install_path TEXT NOT NULL,
    scope TEXT NOT NULL CHECK(scope IN ('user', 'project')),
    workspace_id TEXT, -- NULL for user scope; canonical workspace ID for project scope
    project_root TEXT, -- Filesystem path to workspace root (for project scope)
    enabled INTEGER NOT NULL DEFAULT 1,
    installed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 12. Installed Components
CREATE TABLE install_components (
    component_id TEXT PRIMARY KEY,
    install_id TEXT NOT NULL REFERENCES installs(install_id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    relative_path TEXT,
    enabled INTEGER NOT NULL DEFAULT 1
);

-- 13. Supervised MCP Providers
CREATE TABLE providers (
    provider_id TEXT PRIMARY KEY,
    install_id TEXT NOT NULL REFERENCES installs(install_id) ON DELETE CASCADE,
    component_id TEXT NOT NULL REFERENCES install_components(component_id),
    mode TEXT NOT NULL CHECK(mode IN ('local-stdio', 'remote-http', 'legacy-sse')),
    runtime_adapter TEXT NOT NULL,
    launch_spec_json TEXT NOT NULL,
    auth_profile_id TEXT REFERENCES auth_profiles(profile_id),
    enabled INTEGER NOT NULL DEFAULT 1,
    autostart INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 14. Active Provider Sessions
CREATE TABLE provider_sessions (
    session_id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(provider_id),
    pid INTEGER,
    protocol_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('starting', 'negotiating', 'ready', 'degraded', 'stopping', 'stopped', 'failed')),
    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat TIMESTAMP
);

-- 15. Discovered Capabilities
CREATE TABLE capabilities (
    capability_id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(provider_id) ON DELETE CASCADE,
    native_name TEXT NOT NULL,
    title TEXT,
    description TEXT,
    schema_fingerprint TEXT NOT NULL,
    input_schema_json TEXT NOT NULL,
    discovered_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'changed', 'disabled'))
);
CREATE UNIQUE INDEX idx_cap_prov_native ON capabilities(provider_id, native_name, schema_fingerprint);

-- 16. Capability Grants
CREATE TABLE capability_grants (
    grant_id TEXT PRIMARY KEY,
    capability_id TEXT NOT NULL REFERENCES capabilities(capability_id) ON DELETE CASCADE,
    schema_fingerprint TEXT NOT NULL,
    cas_tree_digest TEXT,       -- Binding for local provider: verified CAS tree SHA-256
    endpoint_origin TEXT,       -- Binding for remote provider: verified HTTPS origin
    server_version_digest TEXT, -- Binding for remote provider: upstream release digest
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'consumed', 'revoked', 'expired')),
    granted_by TEXT NOT NULL,
    granted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP
);

-- 17. Host Agent Registrations
CREATE TABLE host_registrations (
    host_id TEXT NOT NULL, -- e.g. claude-code, codex, cline, pi-agent, grok-build, opencode
    scope TEXT NOT NULL CHECK(scope IN ('user', 'project')),
    workspace_id TEXT NOT NULL DEFAULT '', -- Empty string for user scope, canonical workspace ID for project scope
    config_path TEXT NOT NULL,
    managed_entry_key TEXT NOT NULL,
    entry_fingerprint TEXT NOT NULL,
    registered_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (host_id, scope, workspace_id)
);

-- 18. Host Config Backups
CREATE TABLE host_backups (
    backup_id TEXT PRIMARY KEY,
    host_id TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'user' CHECK(scope IN ('user', 'project')),
    workspace_id TEXT NOT NULL DEFAULT '',
    original_path TEXT NOT NULL,
    backup_path TEXT NOT NULL,
    pre_edit_digest TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 19. Local Audit Log
CREATE TABLE audit_events (
    event_id TEXT PRIMARY KEY,
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target_ref TEXT NOT NULL,
    decision TEXT,
    approval_id TEXT,
    operation_id TEXT,
    outcome TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_time ON audit_events(timestamp);

-- 20. Persistent Auth Profiles
CREATE TABLE auth_profiles (
    profile_id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    profile_type TEXT NOT NULL CHECK(profile_type IN ('oauth2', 'api_key', 'bearer', 'basic', 'custom')),
    secret_ref TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'valid' CHECK(status IN ('valid', 'expired', 'revoked', 'pending_auth')),
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 21. OAuth Sessions
CREATE TABLE oauth_sessions (
    session_id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(provider_id),
    auth_profile_id TEXT NOT NULL REFERENCES auth_profiles(profile_id),
    state_token TEXT NOT NULL UNIQUE,
    pkce_verifier TEXT,
    status TEXT NOT NULL CHECK(status IN ('pending', 'exchanged', 'failed', 'expired')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL
);

-- 22. Operation Tree Ownership Tracker (CAS Safe Rollback)
CREATE TABLE operation_trees (
    operation_id TEXT NOT NULL REFERENCES operations(operation_id) ON DELETE CASCADE,
    tree_digest TEXT NOT NULL,
    created_by_op INTEGER NOT NULL DEFAULT 0, -- 1 if this operation created the tree, 0 if re-used pre-existing tree
    PRIMARY KEY (operation_id, tree_digest)
);
```

---

## 3. The 14-State Operation Journal

```text
created ──► resolving ──► awaiting_approval ──► approved ──► fetching ──► verified ──► staging
                                                                                         │
                                                                                         ▼
committed ◄── committing ◄── commit_intent ◄─────────────────────────────────────────────┘
    ▲
    │ (Upon Failure / Crash)
    └── rolling_back ──► rolled_back | failed | cancelled
```

---

## 4. Crash Recovery Algorithm

On startup, before binding the IPC listener, the daemon calls
`DB.RecoverIncompleteOperations(ctx, stagingRoot, treePath)` (`internal/state/operations.go:243`),
which returns a `RecoverySummary` and an error. If recovery returns an error the daemon **halts**
with exit **1** (`Fatal: startup recovery failed`) rather than serving (`cmd/litespm/main.go:1173-1175`;
REMEDIATION-PLAN finding 102 / `m3`). `70` is a *doctor* category code only (`main.go:932`,
`ARCH/20` §2) and is never returned on this path.

The real decision procedure differs from an earlier draft that referenced a non-existent
`VerifyTreeComplete` / `FinalizeOperationCommit` pair. The implementation is:

```go
// internal/state/operations.go
func (db *DB) RecoverIncompleteOperations(
    ctx context.Context,
    stagingRoot string,
    treePath func(string) string,
) (RecoverySummary, error)

func (db *DB) recoverOperation(
    ctx context.Context, op *OperationRecord, stagingRoot string, treePath func(string) string,
) (string, error) {
    stagingPath := filepath.Join(stagingRoot, op.OperationID)

    switch op.State {
    case "created", "resolving", "awaiting_approval", "approved", "fetching", "staging":
        // No tree was promoted yet: delete staging, mark rolled_back.
        os.RemoveAll(stagingPath)
        db.AdvanceOperationState(ctx, op.OperationID, "rolled_back")

    case "verified", "commit_intent", "committing", "rolling_back":
        // Trees a shared operation re-used are never deleted. Only trees this
        // operation created are considered, and a commit is only finalized as
        // "committed" when an install row already references those trees.
        trees, _ := db.GetOperationTrees(ctx, op.OperationID)
        var createdByOp []string
        for _, t := range trees {
            if t.CreatedByOp {
                createdByOp = append(createdByOp, t.TreeDigest)
            }
        }
        if installExists, _ := db.HasInstallsForTrees(ctx, createdByOp); installExists {
            db.AdvanceOperationState(ctx, op.OperationID, "committed")
            return "committed", nil
        }
        for _, t := range trees {
            if t.CreatedByOp && treePath != nil {
                os.RemoveAll(treePath(t.TreeDigest))
            }
        }
        os.RemoveAll(stagingPath)
        db.AdvanceOperationState(ctx, op.OperationID, "rolled_back")

    default:
        // committed / rolled_back / failed / cancelled are terminal.
    }
    return "", nil
}
```

**Safe rollback invariant:** a CAS tree is deleted only when
`operation_trees.created_by_op = 1` for that operation; a tree that pre-existed
(`created_by_op = 0`) is preserved even when the operation rolls back.
