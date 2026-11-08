package plugin

import "os"

const (
	envPlugin  = "ABSTRAX_PLUGIN"
	envBinary  = "ABSTRAX_BINARY"
	envVersion = "ABSTRAX_VERSION"
)

// IsRunningAsPlugin reports whether the plugin was invoked by the Abstrax CLI.
func IsRunningAsPlugin() bool {
	return os.Getenv(envPlugin) == "1"
}

// HostVersion returns the Abstrax version string supplied by the host CLI.
func HostVersion() string {
	return os.Getenv(envVersion)
}

// HostBinary returns the Abstrax binary path supplied by the host CLI.
func HostBinary() string {
	return os.Getenv(envBinary)
}
