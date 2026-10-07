package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestMigrationsListMatchesCurrentSchemaVersion(t *testing.T) {
	last := Migrations[len(Migrations)-1].Version
	if last != CurrentSchemaVersion {
		t.Fatalf("CurrentSchemaVersion=%d but highest registered migration is v%d", CurrentSchemaVersion, last)
	}
	for i, m := range Migrations {
		if m.Version != i+1 {
			t.Errorf("migration %d has version %d; versions must be contiguous", i, m.Version)
		}
		if strings.TrimSpace(m.UpSQL) == "" {
			t.Errorf("migration v%d has empty SQL (embed missing?)", m.Version)
		}
	}
}

func indexNames(t *testing.T, db *DB) map[string]bool {
	t.Helper()
	rows, err := db.raw.Query(`SELECT name FROM sqlite_master WHERE type='index';`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		out[n] = true
	}
	return out
}

func TestFreshDBAppliesPhase1Migration(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.raw.QueryRow(`SELECT MAX(version) FROM schema_migrations;`).Scan(&v); err != nil || v != CurrentSchemaVersion {
		t.Fatalf("schema version = %d (err %v), want %d", v, err, CurrentSchemaVersion)
	}
	idx := indexNames(t, db)
	for _, want := range []string{"idx_installs_tree_digest", "idx_operations_state"} {
		if !idx[want] {
			t.Errorf("index %s missing", want)
		}
	}
	// installs.kind accepts every documented kind, including agent.
	for _, k := range []domain.ListingKind{domain.KindMCP, domain.KindSkill, domain.KindPlugin, domain.KindAgent} {
		rec := &domain.InstallRecord{InstallID: "inst_" + string(k), ListingID: "l", Kind: k, Version: "1",
			TreeDigest: "sha256:x", InstallPath: "/p/" + string(k), Scope: domain.ScopeUser,
			Status: domain.InstallActive, InstalledAt: time.Now(), UpdatedAt: time.Now()}
		if err := db.SaveInstall(context.Background(), rec); err != nil {
			t.Fatalf("SaveInstall kind %s: %v", k, err)
		}
		got, err := db.GetInstall(context.Background(), rec.InstallID)
		if err != nil || got.Kind != k || got.InstallPath != "/p/"+string(k) {
			t.Errorf("round trip kind=%s: %+v err=%v", k, got, err)
		}
	}
	if err := db.SaveInstall(context.Background(), &domain.InstallRecord{InstallID: "bad", ListingID: "l",
		Kind: "bogus", Version: "1", Scope: domain.ScopeUser}); err == nil {
		t.Error("unknown install kind must be rejected")
	}
}

// TestUpgradeFromV1PreservesChildRows builds a v1 database with children of
// installs, then opens it with the current binary. The table rebuild must not
// cascade-delete install_components / providers / capabilities / grants.
func TestUpgradeFromV1PreservesChildRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	raw, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		initialSchemaSQL,
		`INSERT INTO schema_migrations (version, description) VALUES (1, '001_initial_schema');`,
		`INSERT INTO installs (install_id, listing_id, kind, version, tree_digest, install_path, scope) VALUES ('i1','l1','mcp','1','sha256:a','/old','user');`,
		`INSERT INTO install_components (component_id, install_id, kind, name) VALUES ('i1','i1','mcp-provider','srv');`,
		`INSERT INTO providers (provider_id, install_id, component_id, mode, runtime_adapter, launch_spec_json) VALUES ('p1','i1','i1','local-stdio','process','{}');`,
		`INSERT INTO capabilities (capability_id, provider_id, native_name, schema_fingerprint, input_schema_json) VALUES ('c1','p1','tool','fp','{}');`,
		`INSERT INTO capability_grants (grant_id, capability_id, schema_fingerprint, granted_by) VALUES ('g1','c1','fp','user');`,
		`INSERT INTO host_registrations (host_id, scope, config_path, managed_entry_key, entry_fingerprint) VALUES ('h','user','/c','litespm','fp_default');`,
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("seed v1: %v\n%s", err, s)
		}
	}
	_ = raw.Close()

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("upgrade open: %v", err)
	}
	defer db.Close()

	for table, want := range map[string]int{
		"installs": 1, "install_components": 1, "providers": 1,
		"capabilities": 1, "capability_grants": 1, "host_registrations": 1,
	} {
		var n int
		if err := db.raw.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != want {
			t.Errorf("%s rows after upgrade = %d (err %v), want %d", table, n, err, want)
		}
	}
	// FK enforcement is back on for pooled connections.
	var fk int
	if err := db.raw.QueryRow(`PRAGMA foreign_keys;`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d after migration, want 1", fk)
	}
	// A pre-migration backup was taken (database had applied v1).
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "db", "state-pre-migration-v1-*.db"))
	if len(backups) != 1 {
		t.Errorf("expected one pre-migration backup, found %v", backups)
	}
	// Agent kind is now legal on the rebuilt table.
	if _, err := db.raw.Exec(`INSERT INTO installs (install_id, listing_id, kind, version, tree_digest, install_path, scope) VALUES ('i2','l','agent','1','d','/p','user');`); err != nil {
		t.Errorf("agent kind rejected after upgrade: %v", err)
	}
}

func TestSaveInstallUpsertPreservesGrantsAndProviders(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	rec := &domain.InstallRecord{InstallID: "inst_a", ListingID: "mcp:x", Kind: domain.KindMCP, Version: "1",
		InstallPath: "/p", Scope: domain.ScopeUser, Status: domain.InstallActive, InstalledAt: now, UpdatedAt: now}
	if err := db.SaveInstall(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{InstallID: "inst_a", Kind: domain.ComponentMCPProvider, ComponentName: "srv"}); err != nil {
		t.Fatal(err)
	}
	comps, err := db.ListInstallComponents(ctx, "inst_a")
	if err != nil || len(comps) != 1 {
		t.Fatalf("components: %v %v", comps, err)
	}
	if comps[0].ComponentID == "inst_a" || comps[0].ComponentID == "" {
		t.Fatalf("component id must be distinct from install id, got %q", comps[0].ComponentID)
	}
	if comps[0].ComponentID != ComponentIDFor("inst_a", domain.ComponentMCPProvider, "srv") {
		t.Errorf("unexpected component id %q", comps[0].ComponentID)
	}
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{ProviderID: "p1", InstallID: "inst_a", ComponentName: "srv", Transport: "stdio", Command: "x", CreatedAt: now}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := db.SaveCapability(ctx, &domain.CapabilityRecord{CapabilityID: "c1", ProviderID: "p1", Name: "t", SchemaFingerprint: "fp", InputSchemaJSON: "{}", DiscoveredAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCapabilityGrant(ctx, &domain.CapabilityGrant{GrantID: "g1", CapabilityID: "c1", SchemaFingerprint: "fp", Status: "active", CreatedAt: now}, "user"); err != nil {
		t.Fatal(err)
	}

	// Re-save everything (a re-install): children must survive.
	rec.Version = "2"
	rec.UpdatedAt = now.Add(time.Second)
	if err := db.SaveInstall(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{InstallID: "inst_a", Kind: domain.ComponentMCPProvider, ComponentName: "srv", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{ProviderID: "p1", InstallID: "inst_a", ComponentName: "srv", Transport: "stdio", Command: "y", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCapability(ctx, &domain.CapabilityRecord{CapabilityID: "c1", ProviderID: "p1", Name: "t", SchemaFingerprint: "fp", InputSchemaJSON: "{}", DiscoveredAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetActiveGrant(ctx, "c1", "fp"); err != nil {
		t.Fatalf("capability grant lost on re-save: %v", err)
	}
	got, _ := db.GetInstall(ctx, "inst_a")
	if got.Version != "2" {
		t.Errorf("upsert did not update version: %+v", got)
	}
	p, err := db.GetProvider(ctx, "p1")
	if err != nil || p.Command != "y" {
		t.Errorf("provider not updated in place: %+v %v", p, err)
	}
}

func TestSaveHostRegistrationRequiresRealFingerprintAndKeysByEntry(t *testing.T) {
	ctx := context.Background()
	db, _ := Open(filepath.Join(t.TempDir(), "state.db"))
	defer db.Close()
	reg := &domain.HostRegistrationRecord{HostID: "claude-code", Scope: domain.ScopeUser, ConfigPath: "/c", ManagedEntryKey: "a", RegisteredAt: time.Now()}
	if err := db.SaveHostRegistration(ctx, reg); err == nil {
		t.Fatal("empty fingerprint must be refused, not stored as a placeholder")
	}
	reg.EntryFingerprint = "sha256:1"
	if err := db.SaveHostRegistration(ctx, reg); err != nil {
		t.Fatal(err)
	}
	reg2 := *reg
	reg2.ManagedEntryKey, reg2.EntryFingerprint = "b", "sha256:2"
	if err := db.SaveHostRegistration(ctx, &reg2); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.raw.QueryRow(`SELECT COUNT(*) FROM host_registrations`).Scan(&n)
	if n != 2 {
		t.Errorf("two entries on one host must keep two rows, got %d", n)
	}
}

func TestAuditEventIDsDoNotCollide(t *testing.T) {
	ctx := context.Background()
	db, _ := Open(filepath.Join(t.TempDir(), "state.db"))
	defer db.Close()
	for i := 0; i < 500; i++ {
		if err := db.RecordAuditEvent(ctx, "a", "act", "t", "", "", "", "ok", "{}"); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
}

func TestPreMigrationBackupFailureIsAnError(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Point the handle at a database file that does not exist: the backup must
	// fail loudly instead of returning nil.
	db.dbPath = filepath.Join(dir, "gone.db")
	if err := db.backupDatabaseFile(filepath.Join(dir, "b"), 1); err == nil {
		t.Fatal("backup of a missing database file must return an error")
	}
	// A backup directory that cannot be created is also an error.
	blocker := filepath.Join(dir, "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0600)
	db.dbPath = filepath.Join(dir, "state.db")
	if err := db.backupDatabaseFile(filepath.Join(blocker, "sub"), 1); err == nil {
		t.Fatal("backup into an uncreatable directory must return an error")
	}
}
