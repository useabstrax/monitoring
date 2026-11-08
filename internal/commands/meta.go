package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/plugin"
)

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Plugin protocol commands",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "metadata",
		Short: "Print plugin metadata JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return plugin.WriteMetadata(os.Stdout, plugin.DefaultMetadata())
		},
	})
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Display plugin version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if globals.JSON {
				return finish("monitoring.version", "Abstrax Monitoring "+plugin.Version, map[string]any{
					"schema_version":   1,
					"version":          plugin.Version,
					"protocol_version": plugin.ProtocolVersion,
					"commit":           plugin.Commit,
					"build_date":       plugin.BuildDate,
				})
			}
			fmt.Printf("Plugin version: %s\n", plugin.Version)
			fmt.Printf("Plugin protocol version: %d\n", plugin.ProtocolVersion)
			fmt.Printf("Build commit: %s\n", plugin.Commit)
			fmt.Printf("Build date: %s\n", plugin.BuildDate)
			if plugin.IsRunningAsPlugin() {
				fmt.Printf("Abstrax host version: %s\n", plugin.HostVersion())
			}
			return nil
		},
	}
}
