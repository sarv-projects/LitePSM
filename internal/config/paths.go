package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

// PlatformPaths encapsulates canonical filesystem locations for LiteSPM.
type PlatformPaths struct {
	DataRoot    string
	ConfigRoot  string
	RuntimeRoot string
	PipeName    string // Windows named pipe path, e.g. \\.\pipe\litespm-daemon-<hash>
	SocketPath  string // Unix domain socket path, e.g. /run/user/1000/litespm/litespm.sock
}

// brandNames groups the product-name-dependent path components so the current
// and legacy naming schemes resolve through the same code.
type brandNames struct {
	appDir     string // Windows/macOS application directory (LiteSPM/LitePSM)
	unixDir    string // Linux XDG directory name (litespm/litepsm)
	socketFile string // Unix domain socket file (litespm.sock/litepsm.sock)
	pipePrefix string // Windows named-pipe prefix (litespm/litepsm)
}

var (
	// currentBrand is the post-rename naming scheme used for every new write.
	currentBrand = brandNames{appDir: "LiteSPM", unixDir: "litespm", socketFile: "litespm.sock", pipePrefix: "litespm"}
	// legacyBrand is the pre-rename naming scheme. It is only ever consulted to
	// find an existing installation; the legacy directory is never written
	// unless it has been adopted, and it is never deleted.
	legacyBrand = brandNames{appDir: "LitePSM", unixDir: "litepsm", socketFile: "litepsm.sock", pipePrefix: "litepsm"}
)

// platformRoots computes the per-OS default roots for one naming scheme.
// Environment overrides are applied later, in applyRootOverrides.
func platformRoots(homeDir, username, uid string, brand brandNames) *PlatformPaths {
	paths := &PlatformPaths{}

	switch runtime.GOOS {
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(homeDir, "AppData", "Local")
		}
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}

		paths.DataRoot = filepath.Join(localAppData, brand.appDir)
		paths.ConfigRoot = filepath.Join(appData, brand.appDir)
		paths.RuntimeRoot = paths.DataRoot

		// Hash username to ensure a collision-free pipe name per user
		hasher := sha256.New()
		hasher.Write([]byte(username))
		userHash := hex.EncodeToString(hasher.Sum(nil))[:12]
		paths.PipeName = fmt.Sprintf(`\\.\pipe\%s-daemon-%s`, brand.pipePrefix, userHash)

	case "darwin":
		base := filepath.Join(homeDir, "Library", "Application Support", brand.appDir)
		paths.DataRoot = base
		paths.ConfigRoot = base
		paths.RuntimeRoot = filepath.Join(homeDir, "Library", "Caches", brand.appDir, "run")
		paths.SocketPath = filepath.Join(paths.RuntimeRoot, brand.socketFile)

	default: // Linux and other Unixes
		xdgData := os.Getenv("XDG_DATA_HOME")
		if xdgData == "" {
			xdgData = filepath.Join(homeDir, ".local", "share")
		}
		paths.DataRoot = filepath.Join(xdgData, brand.unixDir)

		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			xdgConfig = filepath.Join(homeDir, ".config")
		}
		paths.ConfigRoot = filepath.Join(xdgConfig, brand.unixDir)

		xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
		if xdgRuntime == "" {
			paths.RuntimeRoot = filepath.Join("/tmp", fmt.Sprintf("%s-%s", brand.unixDir, uid))
		} else {
			paths.RuntimeRoot = filepath.Join(xdgRuntime, brand.unixDir)
		}
		paths.SocketPath = filepath.Join(paths.RuntimeRoot, brand.socketFile)
	}

	return paths
}

// ResolvePlatformPaths detects the current operating system and returns normalized platform paths.
//
// Upgrade compatibility (legacy "LitePSM"/"litepsm" -> current
// "LiteSPM"/"litespm"): an installation created before the rename keeps its
// state.db and CAS under the legacy roots. Switching unconditionally to the new
// roots would strand that state and present a fresh, empty installation, so each
// root independently falls back to the legacy directory when the new directory
// has not been created yet and the legacy one exists.
//
// The fallback is adoption, not migration: nothing is copied, moved or deleted,
// and the legacy root simply continues to be used. If both roots exist the new
// one wins, because that means the installation already migrated forward.
// Explicit environment overrides always win, and the legacy LITEPSM_* variable
// names remain accepted after the current LITESPM_* names, so an upgraded
// install keeps its configured locations.
//
// The Windows named pipe and the Unix socket file are runtime artifacts rather
// than persisted state, so they always use the current names; a new daemon
// creates a fresh endpoint instead of reusing a stale legacy one. The legacy
// root is never removed: after adoption the operator's existing data simply
// stays where it was.
func ResolvePlatformPaths() (*PlatformPaths, error) {
	usr, err := user.Current()
	homeDir := ""
	username := "unknown"
	uid := "1000"
	if err == nil && usr != nil {
		homeDir = usr.HomeDir
		username = usr.Username
		uid = usr.Uid
	}
	if homeDir == "" {
		homeDir = os.Getenv("HOME")
		if homeDir == "" {
			homeDir = os.Getenv("USERPROFILE")
		}
	}

	paths := platformRoots(homeDir, username, uid, currentBrand)
	legacy := platformRoots(homeDir, username, uid, legacyBrand)

	applyRootOverrides(paths, legacy)

	return paths, nil
}

// applyRootOverrides applies environment overrides and legacy-root adoption to
// the resolved roots, then refreshes the derived Unix socket path.
func applyRootOverrides(paths, legacy *PlatformPaths) {
	paths.DataRoot = resolveRoot(paths.DataRoot, legacy.DataRoot, "LITESPM_DATA_ROOT", "LITEPSM_DATA_ROOT")
	paths.ConfigRoot = resolveRoot(paths.ConfigRoot, legacy.ConfigRoot, "LITESPM_CONFIG_ROOT", "LITEPSM_CONFIG_ROOT")
	paths.RuntimeRoot = resolveRoot(paths.RuntimeRoot, legacy.RuntimeRoot, "LITESPM_RUNTIME_ROOT", "LITEPSM_RUNTIME_ROOT")

	if runtime.GOOS != "windows" {
		paths.SocketPath = filepath.Join(paths.RuntimeRoot, currentBrand.socketFile)
	}
}

// resolveRoot picks the effective value for one root, in order of precedence:
//
//  1. the current-name environment override, when set;
//  2. the legacy-name environment override, when set (upgrade fallback);
//  3. the legacy default directory when the new default does not exist and the
//     legacy default does (adoption of an existing installation);
//  4. the new default otherwise.
func resolveRoot(newDefault, legacyDefault, newEnv, legacyEnv string) string {
	if v := os.Getenv(newEnv); v != "" {
		return v
	}
	if v := os.Getenv(legacyEnv); v != "" {
		return v
	}
	if newDefault != legacyDefault && pathMissing(newDefault) && isDir(legacyDefault) {
		return legacyDefault
	}
	return newDefault
}

// pathMissing reports whether nothing exists at p.
func pathMissing(p string) bool {
	if p == "" {
		return true
	}
	_, err := os.Lstat(p)
	return os.IsNotExist(err)
}

// isDir reports whether p exists and is a directory.
func isDir(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// IPCEndpoint returns the OS-specific communication endpoint (Named Pipe on Windows, Domain Socket on Unix).
func (p *PlatformPaths) IPCEndpoint() string {
	if runtime.GOOS == "windows" {
		return p.PipeName
	}
	return p.SocketPath
}

// StateDBPath returns the path to state.db in DATA_ROOT.
func (p *PlatformPaths) StateDBPath() string {
	return filepath.Join(p.DataRoot, "state.db")
}

// DaemonLockPath returns the path to daemon.lock.
func (p *PlatformPaths) DaemonLockPath() string {
	return filepath.Join(p.DataRoot, "daemon.lock")
}

// CASPath returns the directory storing content-addressed files.
func (p *PlatformPaths) CASPath() string {
	return filepath.Join(p.DataRoot, "cas")
}

// BackupsPath returns the directory storing atomic host config backups.
func (p *PlatformPaths) BackupsPath() string {
	return filepath.Join(p.DataRoot, "backups")
}

// StagingPath returns the scratch directory for in-flight extractions and downloads.
func (p *PlatformPaths) StagingPath() string {
	return filepath.Join(p.DataRoot, "staging")
}

// EnsureDirectories creates all standard directories with restrictive permissions (0700).
func (p *PlatformPaths) EnsureDirectories() error {
	dirs := []string{
		p.DataRoot,
		p.ConfigRoot,
		p.RuntimeRoot,
		p.CASPath(),
		p.BackupsPath(),
		p.StagingPath(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}
	return nil
}
