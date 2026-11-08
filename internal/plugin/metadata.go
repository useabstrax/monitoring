package plugin

import (
	"encoding/json"
	"io"
	"strings"
)

const (
	// ProtocolVersion is the supported plugin metadata protocol version.
	ProtocolVersion = 1

	PluginName  = "monitoring"
	BinaryName  = "abstrax-" + PluginName
	DisplayName = "Abstrax Monitoring"
	Description = "Lightweight local Linux monitoring and history for Abstrax servers"
	Homepage    = "https://plugins.useabstrax.com/plugins/monitoring"

	// RequiresAbstrax matches the other official plugins. Monitoring does not
	// depend on a newer CLI contract than plugin dispatch and --json.
	RequiresAbstrax = ">=0.1.0"
)

// MetadataCommand describes a subcommand exposed by the plugin.
type MetadataCommand struct {
	Name        string `json:"name"`
	Action      string `json:"action,omitempty"`
	Description string `json:"description"`
	// Streamable marks commands that emit NDJSON events over time.
	Streamable bool `json:"streamable,omitempty"`
}

// Metadata is the plugin metadata protocol v1 response.
type Metadata struct {
	ProtocolVersion int               `json:"protocol_version"`
	Name            string            `json:"name"`
	DisplayName     string            `json:"display_name"`
	Description     string            `json:"description"`
	Version         string            `json:"version"`
	RequiresAbstrax string            `json:"requires_abstrax"`
	Homepage        string            `json:"homepage,omitempty"`
	Commands        []MetadataCommand `json:"commands"`
}

// DefaultMetadata returns the plugin metadata for this build.
func DefaultMetadata() Metadata {
	return Metadata{
		ProtocolVersion: ProtocolVersion,
		Name:            PluginName,
		DisplayName:     DisplayName,
		Description:     Description,
		Version:         Version,
		RequiresAbstrax: RequiresAbstrax,
		Homepage:        Homepage,
		Commands: []MetadataCommand{
			cmd("setup", "Create configuration, install the service, and start monitoring"),
			cmd("config", "Show or update monitoring configuration"),
			cmd("start", "Start the monitoring service"),
			cmd("stop", "Stop the monitoring service"),
			cmd("restart", "Restart the monitoring service"),
			cmd("status", "Show whether monitoring is healthy"),
			cmd("latest", "Show the newest host, filesystem, and process summary"),
			cmd("history", "Query aligned host and filesystem history"),
			cmd("processes", "List current processes or the processes responsible at a past time"),
			cmd("process", "Show one process instance"),
			cmd("process history", "Query history for one process instance"),
			{
				Name:        "live",
				Action:      "plugin.monitoring.live",
				Description: "Stream live host, filesystem, and top-process samples as NDJSON",
				Streamable:  true,
			},
			cmd("storage status", "Show database size, retention, and rollup tiers"),
			cmd("install-service", "Install and enable the abstrax-monitoring systemd service"),
			cmd("uninstall-service", "Stop and remove the abstrax-monitoring systemd service"),
			cmd("run", "Run the monitoring daemon in the foreground"),
			cmd("version", "Display plugin version information"),
		},
	}
}

func cmd(name, description string) MetadataCommand {
	action := "plugin." + PluginName + "." + strings.ReplaceAll(name, " ", ".")
	action = strings.ReplaceAll(action, "-", "_")
	return MetadataCommand{Name: name, Action: action, Description: description}
}

// WriteMetadata encodes metadata as indented JSON to w.
func WriteMetadata(w io.Writer, metadata Metadata) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(metadata)
}
