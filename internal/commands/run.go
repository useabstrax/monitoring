package commands

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/config"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/daemon"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/layout"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the monitoring daemon in the foreground",
		Args:  cobra.NoArgs,
		RunE:  runDaemon,
	}
}

func runDaemon(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return configExit(err)
	}
	if err := layout.EnsureDir(paths.StateDir); err != nil {
		return configExit(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := daemon.New(cfg, paths, nil, nil, nil, log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP)
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if _, err := d.Reload(); err != nil {
					log.Warn("config reload failed", "err", err)
				} else {
					log.Info("reloaded configuration", "path", paths.ConfigFile)
				}
			}
		}
	}()
	err = d.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
