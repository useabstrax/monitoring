package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/config"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/ipc"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/layout"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/platform"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/service"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Create configuration, install the service, and start monitoring",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := printer()
			p.Progress("monitoring.setup", "platform", "Checking platform support")
			result, err := platform.Check(ctx)
			if err != nil {
				return err
			}
			p.Verbose("platform %s (%s)", result.Family, result.SupportLevel)
			if err := layout.EnsureDir(layoutDir(paths.ConfigFile)); err != nil {
				return err
			}
			if err := layout.EnsureDir(paths.StateDir); err != nil {
				return err
			}
			if _, err := os.Stat(paths.ConfigFile); err == nil {
				p.Info("Keeping existing config %s", paths.ConfigFile)
			} else if os.IsNotExist(err) {
				if err := config.Save(paths.ConfigFile, config.Default()); err != nil {
					return configExit(err)
				}
				p.Info("Wrote %s", paths.ConfigFile)
			} else {
				return err
			}
			p.Progress("monitoring.setup", "service", "Installing the systemd service")
			if err := installUnit(true); err != nil {
				return err
			}
			p.Progress("monitoring.setup", "verify", "Waiting for the first sample")
			deadline := time.Now().Add(20 * time.Second)
			var last error
			for time.Now().Before(deadline) {
				raw, err := call(ctx, ipc.Request{Op: "status"})
				if err == nil {
					return finish("monitoring.setup", "Monitoring is collecting", map[string]any{
						"schema_version": 1,
						"config":         paths.ConfigFile,
						"state_dir":      paths.StateDir,
						"status":         jsonRaw(raw),
					})
				}
				last = err
				time.Sleep(500 * time.Millisecond)
			}
			if last != nil {
				return fmt.Errorf("service installed but monitoring is not answering: %w", last)
			}
			return fmt.Errorf("service installed but monitoring is not answering")
		},
	}
}

func layoutDir(file string) string {
	i := strings.LastIndex(file, "/")
	if i <= 0 {
		return "."
	}
	return file[:i]
}

func confirm(prompt string) bool {
	if globals.Yes {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func newConfigCmd() *cobra.Command {
	var (
		host, proc, fs, raw, short, medium, long, maximum string
		rollShort, rollMedium, rollLong, rollArchive      string
		maxStorage, cmdMax, fsTimeout                     string
	)
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or update monitoring configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadOrDefault()
			if err != nil {
				return configExit(err)
			}
			changed := false
			setDur := func(flag, value string, dest *config.Duration) error {
				if !cmd.Flags().Changed(flag) {
					return nil
				}
				d, err := config.ParseDuration(value)
				if err != nil {
					return err
				}
				*dest = d
				changed = true
				return nil
			}
			if err := setDur("host-interval", host, &cfg.Sampling.HostInterval); err != nil {
				return err
			}
			if err := setDur("process-interval", proc, &cfg.Sampling.ProcessInterval); err != nil {
				return err
			}
			if err := setDur("filesystem-interval", fs, &cfg.Sampling.FilesystemInterval); err != nil {
				return err
			}
			if err := setDur("retention-raw", raw, &cfg.Retention.Raw); err != nil {
				return err
			}
			if err := setDur("retention-short", short, &cfg.Retention.Short); err != nil {
				return err
			}
			if err := setDur("retention-medium", medium, &cfg.Retention.Medium); err != nil {
				return err
			}
			if err := setDur("retention-long", long, &cfg.Retention.Long); err != nil {
				return err
			}
			if err := setDur("retention-maximum", maximum, &cfg.Retention.Maximum); err != nil {
				return err
			}
			if err := setDur("rollup-short", rollShort, &cfg.Rollups.ShortInterval); err != nil {
				return err
			}
			if err := setDur("rollup-medium", rollMedium, &cfg.Rollups.MediumInterval); err != nil {
				return err
			}
			if err := setDur("rollup-long", rollLong, &cfg.Rollups.LongInterval); err != nil {
				return err
			}
			if err := setDur("rollup-archive", rollArchive, &cfg.Rollups.ArchiveInterval); err != nil {
				return err
			}
			if err := setDur("filesystem-timeout", fsTimeout, &cfg.Filesystems.Timeout); err != nil {
				return err
			}
			if cmd.Flags().Changed("max-storage") {
				sz, err := config.ParseSize(maxStorage)
				if err != nil {
					return err
				}
				cfg.Storage.MaxSize = sz
				changed = true
			}
			if cmd.Flags().Changed("command-line-max") {
				n, err := parseIntFlag(cmdMax)
				if err != nil {
					return fmt.Errorf("--command-line-max: %w", err)
				}
				cfg.Process.CommandLineMaxBytes = n
				changed = true
			}
			if !changed {
				if raw, err := call(context.Background(), ipc.Request{Op: "config"}); err == nil {
					return finish("monitoring.config", "Monitoring configuration", jsonRaw(raw))
				}
				return finish("monitoring.config", "Monitoring configuration", viewOf(cfg))
			}
			if _, err := os.Stat(paths.ConfigFile); err == nil && !globals.Yes && !confirm("Replace monitoring config "+paths.ConfigFile+"?") {
				return fmt.Errorf("config not changed")
			}
			if err := config.Save(paths.ConfigFile, cfg); err != nil {
				return configExit(err)
			}
			if raw, err := call(context.Background(), ipc.Request{Op: "reload"}); err == nil {
				return finish("monitoring.config", "Updated monitoring configuration", jsonRaw(raw))
			}
			return finish("monitoring.config", "Updated monitoring configuration; it applies when the daemon next starts", viewOf(cfg))
		},
	}
	cmd.Flags().StringVar(&host, "host-interval", "", "Host sampling interval (for example 5s)")
	cmd.Flags().StringVar(&proc, "process-interval", "", "Process sampling interval")
	cmd.Flags().StringVar(&fs, "filesystem-interval", "", "Filesystem sampling interval")
	cmd.Flags().StringVar(&raw, "retention-raw", "", "How long to keep raw samples")
	cmd.Flags().StringVar(&short, "retention-short", "", "How long to keep short rollups")
	cmd.Flags().StringVar(&medium, "retention-medium", "", "How long to keep medium rollups")
	cmd.Flags().StringVar(&long, "retention-long", "", "How long to keep long rollups")
	cmd.Flags().StringVar(&maximum, "retention-maximum", "", "Maximum history age")
	cmd.Flags().StringVar(&rollShort, "rollup-short", "", "Short rollup width")
	cmd.Flags().StringVar(&rollMedium, "rollup-medium", "", "Medium rollup width")
	cmd.Flags().StringVar(&rollLong, "rollup-long", "", "Long rollup width")
	cmd.Flags().StringVar(&rollArchive, "rollup-archive", "", "Archive rollup width")
	cmd.Flags().StringVar(&maxStorage, "max-storage", "", "Database size ceiling (for example 1GB)")
	cmd.Flags().StringVar(&cmdMax, "command-line-max", "", "Maximum stored command-line bytes")
	cmd.Flags().StringVar(&fsTimeout, "filesystem-timeout", "", "Per-mount statfs timeout")
	return cmd
}

func loadOrDefault() (*config.Config, error) {
	cfg, err := config.Load(paths.ConfigFile)
	if err == nil {
		return cfg, nil
	}
	if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
		return config.Default(), nil
	}
	return nil, err
}

func viewOf(cfg *config.Config) any {
	return map[string]any{
		"schema_version":              1,
		"host_interval_seconds":       cfg.Sampling.HostInterval.Seconds(),
		"process_interval_seconds":    cfg.Sampling.ProcessInterval.Seconds(),
		"filesystem_interval_seconds": cfg.Sampling.FilesystemInterval.Seconds(),
		"retention_raw_seconds":       cfg.Retention.Raw.Seconds(),
		"retention_short_seconds":     cfg.Retention.Short.Seconds(),
		"retention_medium_seconds":    cfg.Retention.Medium.Seconds(),
		"retention_long_seconds":      cfg.Retention.Long.Seconds(),
		"retention_maximum_seconds":   cfg.Retention.Maximum.Seconds(),
		"rollup_short_seconds":        cfg.Rollups.ShortInterval.Seconds(),
		"rollup_medium_seconds":       cfg.Rollups.MediumInterval.Seconds(),
		"rollup_long_seconds":         cfg.Rollups.LongInterval.Seconds(),
		"rollup_archive_seconds":      cfg.Rollups.ArchiveInterval.Seconds(),
		"max_storage_bytes":           cfg.Storage.MaxSize.Bytes(),
		"command_line_max_bytes":      cfg.Process.CommandLineMaxBytes,
		"filesystem_timeout_seconds":  cfg.Filesystems.Timeout.Seconds(),
		"filesystem_exclude":          cfg.Filesystems.Exclude,
	}
}

func parseIntFlag(s string) (int, error) {
	var n int
	_, err := fmt.Sscan(s, &n)
	return n, err
}

func newInstallServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-service",
		Short: "Install and enable the abstrax-monitoring systemd service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := installUnit(true); err != nil {
				return err
			}
			return finish("monitoring.install_service", "Installed "+service.UnitName, map[string]any{
				"schema_version": 1,
				"unit":           service.UnitName,
				"state":          service.ActiveState(),
			})
		},
	}
}

func newUninstallServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall-service",
		Short: "Stop and remove the abstrax-monitoring systemd service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := service.Uninstall(service.UnitPath); err != nil {
				return err
			}
			return finish("monitoring.uninstall_service", "Removed "+service.UnitName, map[string]any{
				"schema_version": 1,
				"unit":           service.UnitName,
			})
		},
	}
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use: "start", Short: "Start the monitoring service", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := service.Systemctl("start", service.UnitName); err != nil {
				return err
			}
			return finish("monitoring.start", "Started monitoring", map[string]any{"schema_version": 1, "state": service.ActiveState()})
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use: "stop", Short: "Stop the monitoring service", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := service.Systemctl("stop", service.UnitName); err != nil {
				return err
			}
			return finish("monitoring.stop", "Stopped monitoring", map[string]any{"schema_version": 1, "state": service.ActiveState()})
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use: "restart", Short: "Restart the monitoring service", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := service.Systemctl("restart", service.UnitName); err != nil {
				return err
			}
			return finish("monitoring.restart", "Restarted monitoring", map[string]any{"schema_version": 1, "state": service.ActiveState()})
		},
	}
}

func installUnit(enable bool) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	unit, err := service.RenderUnit(service.UnitOptions{
		Binary: bin, ConfigFile: paths.ConfigFile, StateDir: paths.StateDir,
	})
	if err != nil {
		return err
	}
	return service.Install(service.UnitPath, unit, enable)
}
