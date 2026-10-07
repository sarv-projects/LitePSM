package config

import (
	"errors"
	"fmt"
	"os"
)

// ErrUnsafeRuntimeRoot is the sentinel for a runtime root that cannot be
// proven safe to bind a socket in. Callers can test for it with errors.Is
// without depending on the concrete error type:
//
//	if err := paths.EnsureDirectories(); errors.Is(err, config.ErrUnsafeRuntimeRoot) { ... }
var ErrUnsafeRuntimeRoot = errors.New("unsafe runtime root")

// Reasons carried by UnsafeRuntimeRootError. They are stable strings so both
// tests and operator-facing messages can branch or display on them without
// parsing free text.
const (
	// RuntimeRootReasonSymlink means the path is a symbolic link, so the
	// directory the daemon would bind in is whatever the link's owner chose.
	RuntimeRootReasonSymlink = "symlink"
	// RuntimeRootReasonNotDirectory means a non-directory occupies the path.
	RuntimeRootReasonNotDirectory = "not a directory"
	// RuntimeRootReasonForeignOwner means the directory exists but belongs to
	// a different user id than the current process.
	RuntimeRootReasonForeignOwner = "not owned by the current user"
)

// UnsafeRuntimeRootError fails closed when the runtime root (the directory
// holding the daemon socket) cannot be trusted. It is a distinct type so the
// daemon can report "someone else controls this path" rather than a generic
// mkdir failure, and it matches ErrUnsafeRuntimeRoot for errors.Is.
type UnsafeRuntimeRootError struct {
	Path   string
	Reason string
	Err    error // underlying cause (permissions, stat failure); may be nil
}

func (e *UnsafeRuntimeRootError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("refusing runtime root %q (%s): %v", e.Path, e.Reason, e.Err)
	}
	return fmt.Sprintf("refusing runtime root %q (%s)", e.Path, e.Reason)
}

func (e *UnsafeRuntimeRootError) Unwrap() error { return e.Err }

func (e *UnsafeRuntimeRootError) Is(target error) bool {
	return target == ErrUnsafeRuntimeRoot
}

// ensureRuntimeRoot creates the runtime root when it is absent and always
// proves the result is safe to bind a socket in.
//
// Why this is not a plain MkdirAll: on Linux the fallback runtime root lives
// in the world-writable /tmp (`/tmp/litespm-<uid>`), where any local user can
// pre-create the path — as a symlink into a directory they control, or as a
// directory they own. MkdirAll happily follows or reuses both, and the daemon
// would then place its socket (and the clients that trust it) inside an
// attacker-controlled directory. So the path is lstat'ed (never stat'ed):
// symlinks and directories owned by another uid are refused outright, an
// owned directory is tightened back to 0700, and anything else fails closed
// with *UnsafeRuntimeRootError.
func ensureRuntimeRoot(dir string) error {
	if dir == "" {
		return nil
	}

	fi, err := os.Lstat(dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect runtime root %q: %w", dir, err)
	}
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(dir, 0700); mkErr != nil {
			return fmt.Errorf("create runtime root %q: %w", dir, mkErr)
		}
		// Re-lstat after creation: MkdirAll may have raced with — or followed —
		// a link planted at the path between the check above and the mkdir, so
		// the created entry is verified exactly like a pre-existing one. In a
		// sticky directory (e.g. /tmp) an entry we just created cannot be
		// replaced by another user, which is what makes this second check the
		// end of the race rather than the start of a new one.
		fi, err = os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("re-inspect runtime root %q: %w", dir, err)
		}
	}
	return verifyRuntimeRoot(dir, fi)
}

// verifyRuntimeRoot applies the safety rules to an already-lstat'ed runtime
// root: no symlinks, no non-directories, no directories owned by another user
// (ownership is only verifiable where the platform reports it — see
// fileOwnerUID), and mode 0700 for anything we do accept.
func verifyRuntimeRoot(dir string, fi os.FileInfo) error {
	if fi.Mode()&os.ModeSymlink != 0 {
		return &UnsafeRuntimeRootError{Path: dir, Reason: RuntimeRootReasonSymlink}
	}
	if !fi.IsDir() {
		return &UnsafeRuntimeRootError{Path: dir, Reason: RuntimeRootReasonNotDirectory}
	}
	owner, known := fileOwnerUID(fi)
	if !known {
		// The platform does not expose file ownership (e.g. Windows); the
		// symlink and type checks above still apply, and mode bits are not a
		// meaningful access control there, so no chmod is attempted.
		return nil
	}
	if owner != currentUID() {
		return &UnsafeRuntimeRootError{
			Path:   dir,
			Reason: fmt.Sprintf("%s (owned by uid %d, current uid %d)", RuntimeRootReasonForeignOwner, owner, currentUID()),
		}
	}
	// Owned by us: tighten a loosened mode (an older install may have created
	// it 0755) back to owner-only before a socket is bound inside.
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("chmod 0700 runtime root %q: %w", dir, err)
	}
	return nil
}
