//go:build !unix

package config

import "os"

// fileOwnerUID has no representation off unix: Windows ACL ownership is not a
// uid, so the ownership branch of verifyRuntimeRoot is skipped there (the
// symlink and directory-type checks still run).
func fileOwnerUID(os.FileInfo) (uint32, bool) { return 0, false }

// currentUID is unused when fileOwnerUID reports no owner; it exists so the
// shared verification code compiles everywhere.
func currentUID() uint32 { return 0 }
