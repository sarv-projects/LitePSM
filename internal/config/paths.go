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

// PlatformPaths encapsulates canonical filesystem locations for LitePSM.
type PlatformPaths struct {
	DataRoot    string
	ConfigRoot  string
	RuntimeRoot string
	PipeName    string // Windows named pipe path, e.g. \\.\pipe\litepsm-daemon-<hash>
	SocketPath  string // Unix domain socket path, e.g. /run/user/1000/litepsm/litepsm.sock
}

// ResolvePlatformPaths detects the current operating system and returns normalized platform paths.
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

		paths.DataRoot = filepath.Join(localAppData, "LitePSM")
		paths.ConfigRoot = filepath.Join(appData, "LitePSM")
		paths.RuntimeRoot = paths.DataRoot

		// Hash username to ensure a collision-free pipe name per user
		hasher := sha256.New()
		hasher.Write([]byte(username))
		userHash := hex.EncodeToString(hasher.Sum(nil))[:12]
		paths.PipeName = fmt.Sprintf(`\\.\pipe\litepsm-daemon-%s`, userHash)

	case "darwin":
		paths.DataRoot = filepath.Join(homeDir, "Library", "Application Support", "LitePSM")
		paths.ConfigRoot = filepath.Join(homeDir, "Library", "Application Support", "LitePSM")
		paths.RuntimeRoot = filepath.Join(homeDir, "Library", "Caches", "LitePSM", "run")
		paths.SocketPath = filepath.Join(paths.RuntimeRoot, "litepsm.sock")

	default: // Linux and other Unixes
		xdgData := os.Getenv("XDG_DATA_HOME")
		if xdgData == "" {
			xdgData = filepath.Join(homeDir, ".local", "share")
		}
		paths.DataRoot = filepath.Join(xdgData, "litepsm")

		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			xdgConfig = filepath.Join(homeDir, ".config")
		}
		paths.ConfigRoot = filepath.Join(xdgConfig, "litepsm")

		xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
		if xdgRuntime == "" {
			paths.RuntimeRoot = filepath.Join("/tmp", fmt.Sprintf("litepsm-%s", uid))
		} else {
			paths.RuntimeRoot = filepath.Join(xdgRuntime, "litepsm")
		}
		paths.SocketPath = filepath.Join(paths.RuntimeRoot, "litepsm.sock")
	}

	// Environment overrides take precedence if set
	if envData := os.Getenv("LITEPSM_DATA_ROOT"); envData != "" {
		paths.DataRoot = envData
	}
	if envConfig := os.Getenv("LITEPSM_CONFIG_ROOT"); envConfig != "" {
		paths.ConfigRoot = envConfig
	}
	if envRuntime := os.Getenv("LITEPSM_RUNTIME_ROOT"); envRuntime != "" {
		paths.RuntimeRoot = envRuntime
		if runtime.GOOS != "windows" {
			paths.SocketPath = filepath.Join(paths.RuntimeRoot, "litepsm.sock")
		}
	}

	return paths, nil
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
