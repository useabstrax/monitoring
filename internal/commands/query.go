package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/api"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/ipc"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
)

func jsonRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{"schema_version": api.SchemaVersion}
	}
	return raw
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether monitoring is healthy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := call(cmd.Context(), ipc.Request{Op: "status"})
			if err != nil {
				if moniterr.Code(err) == "permission_denied" {
					return err
				}
				return finish("monitoring.status", "Monitoring daemon is not running", map[string]any{
					"schema_version": api.SchemaVersion,
					"daemon_state":   "stopped",
					"collecting":     false,
					"message":        err.Error(),
				})
			}
			var st api.Status
			_ = json.Unmarshal(raw, &st)
			summary := "Monitoring is running"
			if !st.Collecting {
				summary = "Monitoring is running and waiting for the first sample"
			}
			if st.StorageConstrained {
				summary += "; history is shorter than configured because the database reached its size limit"
			}
			return finish("monitoring.status", summary, raw)
		},
	}
}

func newLatestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "latest",
		Short: "Show the newest host, filesystem, and process summary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := call(cmd.Context(), ipc.Request{Op: "latest"})
			if err != nil {
				return err
			}
			return finish("monitoring.latest", "Latest monitoring sample", raw)
		},
	}
}

func newHistoryCmd() *cobra.Command {
	var from, to, cursor string
	var maxPoints int
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Query aligned host and filesystem history",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" || to == "" {
				return fmt.Errorf("--from and --to are required")
			}
			raw, err := call(cmd.Context(), ipc.Request{
				Op: "history", From: from, To: to, MaxPoints: maxPoints, Cursor: cursor,
			})
			if err != nil {
				return err
			}
			return finish("monitoring.history", "Monitoring history", raw)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Range start (RFC3339)")
	cmd.Flags().StringVar(&to, "to", "", "Range end (RFC3339)")
	cmd.Flags().IntVar(&maxPoints, "max-points", 0, "Maximum host points in this page")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Opaque cursor from the previous page")
	return cmd
}

func newProcessesCmd() *cobra.Command {
	var at, sortKey, name string
	var limit int
	cmd := &cobra.Command{
		Use:   "processes",
		Short: "List current processes or the processes responsible at a past time",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := call(cmd.Context(), ipc.Request{
				Op: "processes", At: at, Sort: sortKey, Name: name, Limit: limit,
			})
			if err != nil {
				return err
			}
			summary := "Current processes"
			if at != "" {
				summary = "Processes at " + at
			}
			return finish("monitoring.processes", summary, raw)
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "Show the processes responsible at this RFC3339 time")
	cmd.Flags().StringVar(&sortKey, "sort", "cpu", "Sort by cpu, memory, rss, read, or write")
	cmd.Flags().StringVar(&name, "name", "", "Filter by process name or command line")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum processes to return")
	return cmd
}

func newProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process <instance-id>",
		Short: "Show one process instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := call(cmd.Context(), ipc.Request{Op: "process", InstanceID: args[0]})
			if err != nil {
				return err
			}
			return finish("monitoring.process", "Process "+args[0], raw)
		},
	}
	cmd.AddCommand(newProcessHistoryCmd())
	return cmd
}

func newProcessHistoryCmd() *cobra.Command {
	var from, to, cursor string
	var maxPoints int
	cmd := &cobra.Command{
		Use:   "history <instance-id>",
		Short: "Query history for one process instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" || to == "" {
				return fmt.Errorf("--from and --to are required")
			}
			raw, err := call(cmd.Context(), ipc.Request{
				Op: "process_history", InstanceID: args[0],
				From: from, To: to, MaxPoints: maxPoints, Cursor: cursor,
			})
			if err != nil {
				return err
			}
			return finish("monitoring.process.history", "Process history", raw)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Range start (RFC3339)")
	cmd.Flags().StringVar(&to, "to", "", "Range end (RFC3339)")
	cmd.Flags().IntVar(&maxPoints, "max-points", 0, "Maximum points in this page")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Opaque cursor from the previous page")
	return cmd
}

func newStorageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Local monitoring storage",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show database size, retention, and rollup tiers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := call(cmd.Context(), ipc.Request{Op: "storage"})
			if err != nil {
				return err
			}
			return finish("monitoring.storage.status", "Monitoring storage", raw)
		},
	})
	return cmd
}

func newLiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "live",
		Short: "Stream live host, filesystem, and top-process samples as NDJSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if globals.JSON {
				return fmt.Errorf("live streams NDJSON; use --json-stream instead of --json")
			}
			ctx, cancel := notifyContext()
			defer cancel()
			p := printer()
			err := client().Live(ctx, func(frame []byte) error {
				if globals.JSONStream || globals.JSON {
					osWrite(frame)
					return nil
				}
				var sample api.LiveSample
				if err := json.Unmarshal(frame, &sample); err != nil {
					osWrite(frame)
					return nil
				}
				p.Line("%s cpu %.1f%% mem %.1f%% load %.2f processes %d",
					sample.Timestamp, sample.Host.CPUUsagePct, sample.Host.MemoryUsagePct,
					sample.Host.Load1m, sample.ProcessCount)
				return nil
			})
			return err
		},
	}
}

func osWrite(frame []byte) {
	if len(frame) == 0 || frame[len(frame)-1] != '\n' {
		frame = append(append([]byte(nil), frame...), '\n')
	}
	_, _ = os.Stdout.Write(frame)
}
