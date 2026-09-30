package provider

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRingBuffer_BoundedMemory(t *testing.T) {
	rb := NewRingBuffer(100)

	// Write 50 bytes
	_, err := rb.Write([]byte(strings.Repeat("A", 50)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.String()) != 50 {
		t.Errorf("expected 50 bytes, got %d", len(rb.String()))
	}

	// Write 80 bytes (total 130 -> must truncate oldest 30 bytes)
	_, err = rb.Write([]byte(strings.Repeat("B", 80)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.String()) != 100 {
		t.Errorf("expected buffer capped at 100 bytes, got %d", len(rb.String()))
	}
	if !strings.HasPrefix(rb.String(), strings.Repeat("A", 20)) {
		t.Errorf("expected oldest A's partially retained")
	}
	if !strings.HasSuffix(rb.String(), strings.Repeat("B", 80)) {
		t.Errorf("expected newest B's fully retained")
	}

	// Write single payload larger than capacity (200 bytes)
	_, err = rb.Write([]byte(strings.Repeat("C", 200)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.String()) != 100 {
		t.Errorf("expected buffer capped at 100 bytes, got %d", len(rb.String()))
	}
	if rb.String() != strings.Repeat("C", 100) {
		t.Errorf("expected exactly 100 C's")
	}
}

func TestRingBuffer_ConcurrentAccess(t *testing.T) {
	rb := NewRingBuffer(512)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = rb.Write([]byte(fmt.Sprintf("writer-%d-chunk-%d\n", id, j)))
				_ = rb.String()
			}
		}(i)
	}

	wg.Wait()
	if len(rb.String()) > 512 {
		t.Errorf("capacity exceeded: %d bytes", len(rb.String()))
	}
}

func TestSupervisor_Lifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sup := NewSupervisor()

	// Find a standard binary available on the current OS
	var executable string
	var args []string

	if runtime.GOOS == "windows" {
		executable = "cmd.exe"
		args = []string{"/c", "echo hello from provider"}
	} else {
		executable = "/bin/sh"
		args = []string{"-c", "echo hello from provider"}
	}

	handle, err := sup.StartProvider(ctx, "test-prov-1", LaunchSpec{
		Executable: executable,
		Args:       args,
		WorkingDir: os.TempDir(),
	})
	if err != nil {
		t.Fatalf("failed to start provider: %v", err)
	}

	if handle.PID <= 0 {
		t.Errorf("invalid PID: %d", handle.PID)
	}

	// Retrieve provider
	retrieved, err := sup.GetProvider("test-prov-1")
	if err != nil {
		t.Fatalf("failed to retrieve provider: %v", err)
	}
	if retrieved.ProviderID != "test-prov-1" {
		t.Errorf("mismatched provider ID: %s", retrieved.ProviderID)
	}

	// Stop provider
	err = sup.StopProvider(ctx, "test-prov-1")
	if err != nil {
		t.Fatalf("failed to stop provider: %v", err)
	}

	// Test non-existent provider
	_, err = sup.GetProvider("non-existent")
	if err == nil {
		t.Fatal("expected not found error for non-existent provider")
	}
}
