package plugin

// Build information injected via -ldflags during release builds.
var (
	// Version is the plugin semver.
	Version = "0.1.0"
	// Commit is the git commit hash.
	Commit = "none"
	// BuildDate is the UTC build timestamp.
	BuildDate = "unknown"
)
