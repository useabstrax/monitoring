package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/ipc"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/layout"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/output"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/plugin"
)

// GlobalFlags are Abstrax-style globals shared across commands.
type GlobalFlags struct {
	JSON       bool
	JSONStream bool
	Yes        bool
	Verbose    bool
	Quiet      bool
	NoColor    bool
	ConfigFile string
	StateDir   string
}

var (
	globals GlobalFlags
	paths   layout.Paths
)

// NewRootCmd creates the root command for abstrax-monitoring.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   plugin.PluginName,
		Short: "Local Linux monitoring and history",
		Long:  "Official Abstrax Monitoring plugin: collect host and process metrics on this server and keep a local history.",
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "abstrax " + plugin.PluginName,
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if globals.JSON && globals.JSONStream {
				return fmt.Errorf("--json and --json-stream are mutually exclusive")
			}
			p, err := layout.Resolve(globals.ConfigFile, globals.StateDir)
			if err != nil {
				return err
			}
			paths = p
			return nil
		},
	}

	root.PersistentFlags().BoolVar(&globals.JSON, "json", false, "Output a single JSON result object")
	root.PersistentFlags().BoolVar(&globals.JSONStream, "json-stream", false, "Output NDJSON events")
	root.PersistentFlags().BoolVar(&globals.Yes, "yes", false, "Skip confirmation prompts")
	root.PersistentFlags().BoolVar(&globals.Verbose, "verbose", false, "Verbose output")
	root.PersistentFlags().BoolVar(&globals.Quiet, "quiet", false, "Suppress informational messages")
	root.PersistentFlags().BoolVar(&globals.NoColor, "no-color", false, "Disable colored output")
	root.PersistentFlags().StringVar(&globals.ConfigFile, "config", "", "Configuration file (default: /etc/abstrax/plugins/monitoring/config.yaml when root)")
	root.PersistentFlags().StringVar(&globals.StateDir, "state-dir", "", "State directory (default: /var/lib/abstrax/plugins/monitoring when root)")

	root.AddCommand(newPluginCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newSetupCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newRestartCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newLatestCmd())
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newProcessesCmd())
	root.AddCommand(newProcessCmd())
	root.AddCommand(newLiveCmd())
	root.AddCommand(newStorageCmd())
	root.AddCommand(newInstallServiceCmd())
	root.AddCommand(newUninstallServiceCmd())
	root.AddCommand(newRunCmd())

	return root
}

// Execute runs the plugin command tree.
func Execute() int {
	root := NewRootCmd()
	err := root.Execute()
	if err == nil {
		return plugin.ExitSuccess
	}
	code := exitCode(err)
	if globals.JSON || globals.JSONStream {
		printer().Print(output.Failure(guessAction(root), moniterr.Code(err), err.Error()))
		return code
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	if code == plugin.ExitInternal && isUsageError(err) {
		return plugin.ExitUsage
	}
	return code
}

func printer() *output.Printer {
	return output.NewPrinter(globals.JSON, globals.JSONStream, globals.Quiet, globals.Verbose, globals.NoColor)
}

func client() *ipc.Client { return ipc.NewClient(paths.Socket()) }

func call(ctx context.Context, req ipc.Request) (json.RawMessage, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return client().Call(cctx, req)
}

func finish(action, summary string, data any) error {
	printer().Print(output.Success(action, summary, data))
	return nil
}

func exitCode(err error) int {
	var me *moniterr.Error
	if errors.As(err, &me) {
		switch me.Code {
		case "invalid_argument":
			return plugin.ExitUsage
		case "config_invalid":
			return plugin.ExitConfig
		}
	}
	var exitErr *exitError
	if errors.As(err, &exitErr) {
		return exitErr.code
	}
	if isUsageError(err) {
		return plugin.ExitUsage
	}
	return plugin.ExitInternal
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func configExit(err error) error { return &exitError{code: plugin.ExitConfig, err: err} }

func isUsageError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unknown command") ||
		strings.Contains(s, "accepts") ||
		strings.Contains(s, "required") ||
		strings.Contains(s, "invalid argument") ||
		strings.Contains(s, "flag needs an argument") ||
		strings.Contains(s, "unknown flag")
}

func guessAction(root *cobra.Command) string {
	if root == nil {
		return plugin.PluginName
	}
	cmd, _, _ := root.Find(os.Args[1:])
	if cmd == nil || cmd == root {
		return plugin.PluginName
	}
	parts := []string{plugin.PluginName}
	var chain []string
	for c := cmd; c != nil && c != root; c = c.Parent() {
		chain = append([]string{strings.ReplaceAll(c.Name(), "-", "_")}, chain...)
	}
	return strings.Join(append(parts, chain...), ".")
}

func notifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
