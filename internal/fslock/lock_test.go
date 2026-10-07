package fslock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireExcludesAndReleaseReopens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.lock")
	l, err := TryAcquire(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = TryAcquire(p)
	var busy *BusyError
	if !errors.As(err, &busy) || busy.PID != os.Getpid() || !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquire should be busy with our pid, got %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("release did not remove the lock file")
	}
	l2, err := TryAcquire(p)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	_ = l2.Release()
}

func TestReleaseOnlyRemovesOwnLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.lock")
	l1, err := TryAcquire(p)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate: l1's lock was reclaimed (e.g. believed dead) and another holder took over.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	l2, err := TryAcquire(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := l1.Release(); err != nil { // stale holder releasing late
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("a stale holder's Release deleted its successor's lock")
	}
	if err := l2.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("owner release failed to remove lock")
	}
	if err := l1.Release(); err != nil { // idempotent
		t.Fatal(err)
	}
	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestStaleLockOfDeadProcessIsReclaimed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.lock")
	old := aliveFn
	aliveFn = func(pid int) bool { return pid != 424242 }
	defer func() { aliveFn = old }()
	if err := os.WriteFile(p, []byte(encode(424242, "dead")), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := TryAcquire(p)
	if err != nil {
		t.Fatalf("stale lock must be reclaimed: %v", err)
	}
	data, _ := os.ReadFile(p)
	if pid, _, ok := decode(data); !ok || pid != os.Getpid() {
		t.Fatalf("lock not rewritten to our pid: %q", data)
	}
	_ = l.Release()
}

func TestYoungUnparseableLockIsNotStolen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.lock")
	if err := os.WriteFile(p, nil, 0o600); err != nil { // a mid-publish / foreign file
		t.Fatal(err)
	}
	if _, err := TryAcquire(p); !errors.Is(err, ErrBusy) {
		t.Fatalf("empty young lock must not be stolen, got %v", err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(p, old, old)
	l, err := TryAcquire(p)
	if err != nil {
		t.Fatalf("old unparseable lock should be reclaimable: %v", err)
	}
	_ = l.Release()
}

// Many goroutines race to reclaim one stale lock and then hold it; the critical
// section must never be entered concurrently.
func TestConcurrentAcquireIsMutuallyExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.lock")
	old := aliveFn
	aliveFn = func(pid int) bool { return pid != 424242 }
	defer func() { aliveFn = old }()
	_ = os.WriteFile(p, []byte(encode(424242, "dead")), 0o600)

	var inside, violations, entered int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := Acquire(p, Options{Timeout: 10 * time.Second, Poll: time.Millisecond})
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			if atomic.AddInt32(&inside, 1) != 1 {
				atomic.AddInt32(&violations, 1)
			}
			atomic.AddInt32(&entered, 1)
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			_ = l.Release()
		}()
	}
	wg.Wait()
	if violations != 0 || entered != 16 {
		t.Fatalf("violations=%d entered=%d", violations, entered)
	}
}

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("own pid must be alive")
	}
	if ProcessAlive(0) || ProcessAlive(-5) {
		t.Fatal("non-positive pids are never alive")
	}
	// A real child that has exited must be reported dead.
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit 0")
	} else {
		cmd = exec.Command("true")
	}
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn a short-lived child: %v", err)
	}
	if ProcessAlive(cmd.Process.Pid) {
		t.Fatal("an exited child must not be reported alive")
	}
}
