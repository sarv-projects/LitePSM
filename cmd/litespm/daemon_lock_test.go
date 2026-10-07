package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireLock_SingleInstanceAndOwnership(t *testing.T) {
	p := filepath.Join(t.TempDir(), "daemon.lock")

	first, err := acquireLock(p)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// A second daemon must be refused, and the refusal must name the live holder.
	if _, err := acquireLock(p); err == nil || !strings.Contains(err.Error(), "PID") {
		t.Fatalf("second acquire must fail naming the live PID, got %v", err)
	}
	// The failed attempt must not have disturbed the first holder's lock file.
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("a refused acquire removed the live lock: %v", err)
	}

	// Simulate the lock being reclaimed by a successor; our late release must
	// leave the successor's lock alone.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	second, err := acquireLock(p)
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	releaseLock(first)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("releaseLock of a stale holder deleted the current holder's lock")
	}
	releaseLock(second)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("owner releaseLock did not remove the lock")
	}
}

func TestAcquireLock_ReclaimsDeadPIDButNotMalformedYoungFile(t *testing.T) {
	dir := t.TempDir()
	// A pid that cannot be alive: far above any kernel pid_max.
	dead := filepath.Join(dir, "dead.lock")
	if err := os.WriteFile(dead, []byte("2147483646\nolder-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := acquireLock(dead)
	if err != nil {
		t.Fatalf("lock of a dead pid must be reclaimed: %v", err)
	}
	releaseLock(l)

	// An empty lock file (another process mid-write) is not a stale lock.
	young := filepath.Join(dir, "young.lock")
	if err := os.WriteFile(young, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(young); err == nil {
		t.Fatal("an empty young lock file must not be stolen")
	}
}
