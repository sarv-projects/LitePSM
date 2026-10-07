// Package fslock is a small cross-process, advisory lock built on lock files.
//
// It exists for two callers with different needs:
//
//   - the daemon's single-instance lock (fail immediately when a live process
//     holds it), and
//   - the skills ledger's read-modify-write critical section (wait, with a
//     timeout, for another short-lived process to finish).
//
// Properties:
//
//   - Atomic publication. The lock file is written complete under a temporary
//     name and then hard-linked into place, so link(2) either creates the lock
//     with its full content or fails because it exists. A reader can therefore
//     never observe a half-written lock and mistake it for a stale one (the
//     failure mode of "create O_EXCL, then write the pid").
//   - Ownership. Every lock carries a random token. Release removes the file
//     only when it still holds this holder's token, so a process whose lock was
//     legitimately reclaimed never deletes its successor's lock.
//   - Stale recovery. A lock whose recorded pid is not alive is taken over
//     under a short-lived guard lock, re-checking staleness inside the guard so
//     two simultaneous reclaimers cannot both win.
//
// Windows: liveness uses OpenProcess + GetExitCodeProcess (see alive_windows.go).
package fslock

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrBusy is returned when a live process holds the lock.
var ErrBusy = errors.New("lock is held by a live process")

// BusyError carries the holder's pid.
type BusyError struct {
	Path string
	PID  int
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("lock %s is held by live process PID %d", e.Path, e.PID)
}

// Is makes errors.Is(err, ErrBusy) work.
func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// Lock is a held lock. The zero value is not usable.
type Lock struct {
	path  string
	token string
}

// Path returns the lock file path.
func (l *Lock) Path() string { return l.path }

// aliveFn is swapped in tests.
var aliveFn = ProcessAlive

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func encode(pid int, token string) string {
	return fmt.Sprintf("%d\n%s\n", pid, token)
}

// decode parses lock content. ok=false means the content is not a lock record
// (empty, truncated or foreign). Such a lock is never treated as stale by
// itself: only its age can make it reclaimable.
func decode(data []byte) (pid int, token string, ok bool) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return 0, "", false
	}
	p, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || p <= 0 {
		return 0, "", false
	}
	if len(lines) > 1 {
		token = strings.TrimSpace(lines[1])
	}
	return p, token, true
}

// publish atomically creates path with content, failing with os.ErrExist when
// the path already exists.
func publish(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lock-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ErrExist
		}
		// Filesystems without hard links: fall back to O_EXCL create. The
		// content is written immediately; readers additionally ignore
		// unparseable content until it is older than staleUnparseable.
		f, ferr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if ferr != nil {
			if errors.Is(ferr, os.ErrExist) {
				return os.ErrExist
			}
			return ferr
		}
		_, werr := f.WriteString(content)
		cerr := f.Close()
		if werr != nil {
			_ = os.Remove(path)
			return werr
		}
		if cerr != nil {
			_ = os.Remove(path)
			return cerr
		}
	}
	return nil
}

// staleUnparseable is how old a lock file with unreadable content must be
// before it is treated as abandoned.
const staleUnparseable = 30 * time.Second

// stale reports whether the lock at path is reclaimable, with the holder pid
// when known. It re-reads the file, so callers can re-check under a guard.
func stale(path string) (isStale bool, pid int, gone bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, true
		}
		return false, 0, false
	}
	pid, _, ok := decode(data)
	if ok {
		return !aliveFn(pid), pid, false
	}
	if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > staleUnparseable {
		return true, 0, false
	}
	return false, 0, false
}

// TryAcquire takes the lock or fails immediately. A lock held by a live
// process yields a *BusyError; a stale one is reclaimed.
func TryAcquire(path string) (*Lock, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	content := encode(os.Getpid(), token)
	for attempt := 0; attempt < 5; attempt++ {
		err := publish(path, content)
		if err == nil {
			return &Lock{path: path, token: token}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		isStale, pid, gone := stale(path)
		if gone {
			continue // released between our link attempt and the read; retry
		}
		if !isStale {
			if pid == 0 {
				// Unparseable but young: another process is mid-publish
				// (fallback path) or the file is foreign. Do not steal.
				return nil, &BusyError{Path: path, PID: 0}
			}
			return nil, &BusyError{Path: path, PID: pid}
		}
		if err := reclaim(path); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not acquire lock %s (contention): %w", path, ErrBusy)
}

// reclaim removes a stale lock under a guard, re-checking inside the guard.
func reclaim(path string) error {
	guard := path + ".reclaim"
	g, err := publishGuard(guard)
	if err != nil {
		time.Sleep(5 * time.Millisecond) // someone else is reclaiming; the caller retries
		return nil
	}
	defer g()
	if isStale, _, gone := stale(path); gone || !isStale {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale lock %s: %w", path, err)
	}
	return nil
}

func publishGuard(guard string) (release func(), err error) {
	if fi, serr := os.Stat(guard); serr == nil && time.Since(fi.ModTime()) > staleUnparseable {
		_ = os.Remove(guard) // a crashed reclaimer
	}
	if err := publish(guard, encode(os.Getpid(), "guard")); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(guard) }, nil
}

// Options controls Acquire.
type Options struct {
	// Timeout bounds how long to wait for a live holder. Zero means fail
	// immediately (same as TryAcquire).
	Timeout time.Duration
	// Poll is the retry interval. Default 25ms.
	Poll time.Duration
}

// Acquire waits up to opts.Timeout for the lock.
func Acquire(path string, opts Options) (*Lock, error) {
	poll := opts.Poll
	if poll <= 0 {
		poll = 25 * time.Millisecond
	}
	deadline := time.Now().Add(opts.Timeout)
	for {
		l, err := TryAcquire(path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, ErrBusy) || !time.Now().Before(deadline) {
			return nil, err
		}
		time.Sleep(poll)
	}
}

// Release drops the lock if, and only if, this holder still owns it. Releasing
// a lock that was reclaimed by another process is a no-op, never a deletion of
// the successor's lock. Safe to call on a nil Lock and more than once.
func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if _, tok, ok := decode(data); !ok || tok != l.token {
		return nil // not ours any more
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
