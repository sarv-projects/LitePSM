package secrets

import (
	"context"
	"strings"
	"testing"
)

func TestSecretStore_CRUD(t *testing.T) {
	ctx := context.Background()
	store, err := NewMemorySecretStore()
	if err != nil {
		t.Fatalf("NewMemorySecretStore failed: %v", err)
	}
	defer store.Close()

	// 1. Put
	ref, err := store.Put(ctx, "mcp:provider:github", "api_token", []byte("ghp_secret1234567890"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if ref.URI != "secret:mcp:provider:github/api_token" {
		t.Errorf("unexpected URI: %s", ref.URI)
	}

	// 2. Exists
	exists, err := store.Exists(ctx, *ref)
	if err != nil || !exists {
		t.Fatalf("expected secret to exist: %v", err)
	}

	// 3. Get
	val, err := store.Get(ctx, *ref)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "ghp_secret1234567890" {
		t.Fatalf("unexpected secret value: %s", string(val))
	}

	// 4. ListMetadata (Ensure plaintext is NOT in metadata)
	metaList, err := store.ListMetadata(ctx, "mcp:provider:github")
	if err != nil {
		t.Fatalf("ListMetadata failed: %v", err)
	}
	if len(metaList) != 1 {
		t.Fatalf("expected 1 metadata entry, got %d", len(metaList))
	}
	if metaList[0].Key != "api_token" {
		t.Errorf("unexpected metadata key: %s", metaList[0].Key)
	}

	// 5. Delete
	if err := store.Delete(ctx, *ref); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	exists, err = store.Exists(ctx, *ref)
	if err != nil || exists {
		t.Fatalf("expected secret to be deleted")
	}

	_, err = store.Get(ctx, *ref)
	if err == nil {
		t.Fatalf("expected error getting deleted secret")
	}
}

func TestResolveLaunchSecrets(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSecretStore()
	if err != nil {
		t.Fatalf("OpenSecretStore failed: %v", err)
	}
	defer store.Close()

	_, err = store.Put(ctx, "postgres", "password", []byte("db_pass_xyz"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	refs := map[string]string{
		"PGPASSWORD": "secret:postgres/password",
	}

	resolved, err := ResolveLaunchSecrets(ctx, store, refs)
	if err != nil {
		t.Fatalf("ResolveLaunchSecrets failed: %v", err)
	}

	if resolved["PGPASSWORD"] != "db_pass_xyz" {
		t.Errorf("unexpected resolved secret: %s", resolved["PGPASSWORD"])
	}
}

func TestParseSecretRef(t *testing.T) {
	valid := "secret:oauth:google/access_token"
	ref, err := ParseSecretRef(valid)
	if err != nil {
		t.Fatalf("ParseSecretRef failed: %v", err)
	}
	if ref.Namespace != "oauth:google" || ref.Key != "access_token" {
		t.Errorf("unexpected parsed ref: %+v", ref)
	}

	invalid := []string{
		"http://example.com",
		"secret:invalid",
		"secret:/key",
		"secret:namespace/",
	}
	for _, inv := range invalid {
		if _, err := ParseSecretRef(inv); err == nil {
			t.Errorf("expected error parsing %q", inv)
		}
	}
}

func TestZeroPlaintextCanaryLeak(t *testing.T) {
	ctx := context.Background()
	store, err := NewMemorySecretStore()
	if err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	defer store.Close()

	canarySecret := "SUPER_CONFIDENTIAL_TOKEN_XYZ_98765"
	ref, err := store.Put(ctx, "test", "canary", []byte(canarySecret))
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}

	// Verify metadata does not contain canary
	metaList, _ := store.ListMetadata(ctx, "test")
	for _, m := range metaList {
		if strings.Contains(m.URI, canarySecret) || strings.Contains(m.Key, canarySecret) || strings.Contains(m.Namespace, canarySecret) {
			t.Fatalf("canary leaked into metadata!")
		}
	}

	// Verify deleted ref error does not leak canary
	_ = store.Delete(ctx, *ref)
	_, err = store.Get(ctx, *ref)
	if err != nil && strings.Contains(err.Error(), canarySecret) {
		t.Fatalf("canary leaked into error message!")
	}
}
