// Package layout resolves monitoring configuration and state paths.
//
// The daemon listens on a Unix socket inside the plugin state directory.
// Abstrax has no shared runtime directory; the agent keeps its own state
// under /var/lib/abstrax/plugins/agent and does not publish a socket path.
// The socket is therefore plugin-owned:
//
//	/var/lib/abstrax/plugins/monitoring/monitoring.sock
//
// The state directory is mode 0700 and the socket is mode 0600, so local
// access is root-only when the daemon runs as root.
package layout

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	SystemConfigDir = "/etc/abstrax/plugins/monitoring"
	SystemStateDir  = "/var/lib/abstrax/plugins/monitoring"

	ConfigFileName   = "config.yaml"
	DBFileName       = "metrics.db"
	SnapshotFileName = "latest.json"
	SocketFileName   = "monitoring.sock"

	EnvConfigPath = "ABSTRAX_MONITORING_CONFIG"
	EnvStateDir   = "ABSTRAX_MONITORING_STATE_DIR"
)

// Paths are the resolved config and state locations.
type Paths struct {
	ConfigFile string
	StateDir   string
}

// Database returns the SQLite path.
func (p Paths) Database() string { return filepath.Join(p.StateDir, DBFileName) }

// Snapshot returns the latest.json path.
func (p Paths) Snapshot() string { return filepath.Join(p.StateDir, SnapshotFileName) }

// Socket returns the daemon socket path.
func (p Paths) Socket() string { return filepath.Join(p.StateDir, SocketFileName) }

// IsRoot reports whether the current process is running as root.
func IsRoot() bool { return os.Geteuid() == 0 }

// Resolve determines config and state paths from flags, then environment,
// then root or per-user defaults.
func Resolve(configFlag, stateFlag string) (Paths, error) {
	p := Paths{
		ConfigFile: first(configFlag, os.Getenv(EnvConfigPath)),
		StateDir:   first(stateFlag, os.Getenv(EnvStateDir)),
	}
	if p.ConfigFile != "" && p.StateDir != "" {
		return p, nil
	}
	cfgDir, stateDir, err := defaultDirs()
	if err != nil {
		return Paths{}, err
	}
	if p.ConfigFile == "" {
		p.ConfigFile = filepath.Join(cfgDir, ConfigFileName)
	}
	if p.StateDir == "" {
		p.StateDir = stateDir
	}
	return p, nil
}

func defaultDirs() (string, string, error) {
	if IsRoot() {
		return SystemConfigDir, SystemStateDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolving home directory: %w", err)
	}
	cfgBase := first(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
	dataBase := first(os.Getenv("XDG_DATA_HOME"), filepath.Join(home, ".local", "share"))
	return filepath.Join(cfgBase, "abstrax", "plugins", "monitoring"),
		filepath.Join(dataBase, "abstrax", "plugins", "monitoring"), nil
}

// EnsureDir creates dir with mode 0700.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	return os.Chmod(dir, 0o700)
}

// WriteAtomic writes data to path via a temp file in the same directory,
// fsync, chmod, and rename.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
