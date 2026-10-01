package host

import (
	"os"
	"os/user"
	"runtime"
)

// resolveHomeDir returns the current user's home directory.
//
// Environment overrides are honored first so that configuration discovery
// matches the documented user-scope paths (%USERPROFILE% on Windows, $HOME on
// Unix) and can be redirected safely in sandboxed or test environments. The
// platform user lookup is only used as a fallback when no override is present.
func resolveHomeDir() string {
	if runtime.GOOS == "windows" {
		if h := os.Getenv("USERPROFILE"); h != "" {
			return h
		}
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	if usr, err := user.Current(); err == nil && usr != nil {
		return usr.HomeDir
	}
	return ""
}
