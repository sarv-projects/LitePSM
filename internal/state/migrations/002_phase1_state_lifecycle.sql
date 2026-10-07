-- 002_phase1_state_lifecycle: expand install kinds, add lifecycle indexes,
-- and create the deployment mutation ledger (ARCH/33).
--
-- SQLite cannot ALTER a CHECK constraint in place, so the installs table is
-- rebuilt with the full nine-value kind enum. The copy preserves every row;
-- the old table is dropped only after the copy succeeds (same transaction).
--
-- IMPORTANT: this script must run with PRAGMA foreign_keys=OFF on its
-- connection (state.ApplyMigrations does that and then runs
-- PRAGMA foreign_key_check before COMMIT). With enforcement on, DROP TABLE
-- performs an implicit DELETE that would CASCADE away every install_components
-- and providers row (and through them capabilities and capability_grants).

-- 1. Rebuild installs with the full kind enum (plugin|mcp|skill|connector|agent|rule|hook|tool|lsp).
DROP TABLE IF EXISTS installs_new;
CREATE TABLE installs_new (
    install_id TEXT PRIMARY KEY,
    listing_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('plugin', 'mcp', 'skill', 'connector', 'agent', 'rule', 'hook', 'tool', 'lsp')),
    version TEXT NOT NULL,
    immutable_ref TEXT,
    tree_digest TEXT NOT NULL,
    install_path TEXT NOT NULL,
    scope TEXT NOT NULL CHECK(scope IN ('user', 'project')),
    workspace_id TEXT,
    project_root TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    installed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO installs_new
    (install_id, listing_id, kind, version, immutable_ref, tree_digest, install_path, scope, workspace_id, project_root, enabled, installed_at, updated_at)
SELECT install_id, listing_id, kind, version, immutable_ref, tree_digest, install_path, scope, workspace_id, project_root, enabled, installed_at, updated_at
FROM installs;

DROP TABLE installs;
ALTER TABLE installs_new RENAME TO installs;

-- 2. Lifecycle lookup indexes (S1/S2).
CREATE INDEX IF NOT EXISTS idx_installs_tree_digest ON installs(tree_digest);
CREATE INDEX IF NOT EXISTS idx_installs_listing ON installs(listing_id);
CREATE INDEX IF NOT EXISTS idx_operations_state ON operations(state);
CREATE INDEX IF NOT EXISTS idx_install_components_install ON install_components(install_id);
CREATE INDEX IF NOT EXISTS idx_providers_install ON providers(install_id);

-- 3. Deployment mutation ledger (ARCH/33 §2). One row per atomic owned
-- mutation: what LiteSPM wrote, where, and against what pre-state.
CREATE TABLE IF NOT EXISTS deployment_mutations (
    mutation_id TEXT PRIMARY KEY,
    install_id TEXT NOT NULL REFERENCES installs(install_id) ON DELETE CASCADE,
    capability_id TEXT NOT NULL,
    host_id TEXT NOT NULL,
    scope TEXT NOT NULL CHECK(scope IN ('user', 'project')),
    file_path TEXT NOT NULL,
    structure_type TEXT NOT NULL,
    locator TEXT NOT NULL,
    pre_image_hash TEXT NOT NULL,
    post_image_hash TEXT NOT NULL,
    owner TEXT NOT NULL DEFAULT 'litespm',
    -- Raw prior node when the write replaced an existing user-authored entry,
    -- so uninstall can restore it instead of just deleting. '' = none existed.
    prior_entry TEXT NOT NULL DEFAULT '',
    detached INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_mutations_install ON deployment_mutations(install_id);
-- One owned write per target per install: re-installing the same entry updates
-- its row rather than stacking duplicates (see deployment.SaveMutationExec).
CREATE UNIQUE INDEX IF NOT EXISTS uq_mutations_target ON deployment_mutations(install_id, host_id, scope, file_path, locator);
CREATE INDEX IF NOT EXISTS idx_mutations_file ON deployment_mutations(file_path, locator);

-- 4. host_registrations was keyed (host_id, scope, workspace_id), so a second
-- installed MCP server on the same host overwrote the first one's row. Key it
-- by the managed entry as well. Rows are copied verbatim.
DROP TABLE IF EXISTS host_registrations_new;
CREATE TABLE host_registrations_new (
    host_id TEXT NOT NULL,
    scope TEXT NOT NULL CHECK(scope IN ('user', 'project')),
    workspace_id TEXT NOT NULL DEFAULT '',
    config_path TEXT NOT NULL,
    managed_entry_key TEXT NOT NULL,
    entry_fingerprint TEXT NOT NULL,
    registered_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (host_id, scope, workspace_id, managed_entry_key)
);
INSERT INTO host_registrations_new
    (host_id, scope, workspace_id, config_path, managed_entry_key, entry_fingerprint, registered_at)
SELECT host_id, scope, workspace_id, config_path, managed_entry_key, entry_fingerprint, registered_at
FROM host_registrations;
DROP TABLE host_registrations;
ALTER TABLE host_registrations_new RENAME TO host_registrations;
