package plugin

const (
	// ExitSuccess indicates the command completed successfully.
	ExitSuccess = 0
	// ExitInternal indicates an unexpected plugin failure.
	ExitInternal = 1
	// ExitUsage indicates invalid arguments or command usage.
	ExitUsage = 2
	// ExitConfig is the exit status used for unrecoverable configuration
	// problems. The systemd unit sets RestartPreventExitStatus so systemd
	// does not restart-loop on it.
	ExitConfig = 78
)
