// Package service renders and installs the abstrax-monitoring systemd unit.
package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// UnitName is the systemd unit installed by `abstrax monitoring install-service`.
const UnitName = "abstrax-monitoring.service"

// UnitPath is where the unit file is written.
const UnitPath = "/etc/systemd/system/" + UnitName

// ExitConfig is the exit status `run` uses for unrecoverable configuration
// problems. The unit sets RestartPreventExitStatus so systemd does not
// restart-loop on it.
const ExitConfig = 78

// UnitOptions are the values substituted into the unit.
type UnitOptions struct {
	Binary     string
	ConfigFile string
	StateDir   string
}

// Monitoring runs as root. The unit has no filesystem sandbox: it matches
// the agent unit, which also runs unconfined because children inherit the
// sandbox. This daemon has no job children, and it still stays unconfined
// so a later collector that must read every mount is not blocked by a
// sandbox we would then have to punch holes in.
var unitTemplate = template.Must(template.New("unit").Funcs(template.FuncMap{"quote": quote}).Parse(`[Unit]
Description=Abstrax Monitoring
Documentation=https://useabstrax.com/docs/plugins/official/monitoring
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=10

[Service]
Type=simple
User=root
Group=root
ExecStart={{ quote .Binary }} run
Environment={{ quote (print "ABSTRAX_MONITORING_CONFIG=" .ConfigFile) }}
Environment={{ quote (print "ABSTRAX_MONITORING_STATE_DIR=" .StateDir) }}
Restart=on-failure
RestartSec=5s
RestartPreventExitStatus={{ .ExitConfig }}
KillMode=mixed
TimeoutStopSec=30
StandardOutput=journal
StandardError=journal
SyslogIdentifier=abstrax-monitoring

[Install]
WantedBy=multi-user.target
`))

// RenderUnit returns the unit file contents.
func RenderUnit(o UnitOptions) (string, error) {
	for name, p := range map[string]string{
		"monitoring binary": o.Binary,
		"config file":       o.ConfigFile,
		"state dir":         o.StateDir,
	} {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("%s must be an absolute path, got %q", name, p)
		}
		if strings.ContainsAny(p, "\n\r\"\\%$") {
			return "", fmt.Errorf("%s contains characters not allowed in a systemd unit: %q", name, p)
		}
	}
	var buf bytes.Buffer
	err := unitTemplate.Execute(&buf, struct {
		UnitOptions
		ExitConfig int
	}{o, ExitConfig})
	return buf.String(), err
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// Systemctl runs systemctl. Tests replace it.
var Systemctl = func(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Install writes the unit, reloads systemd, and enables and restarts the service.
func Install(path, unit string, enable bool) error {
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := Systemctl("daemon-reload"); err != nil {
		return err
	}
	if !enable {
		return nil
	}
	if err := Systemctl("enable", UnitName); err != nil {
		return err
	}
	return Systemctl("restart", UnitName)
}

// Uninstall stops and disables the service and removes the unit.
func Uninstall(path string) error {
	_ = Systemctl("stop", UnitName)
	_ = Systemctl("disable", UnitName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return Systemctl("daemon-reload")
}

// ActiveState returns `systemctl is-active` output, or "unknown".
func ActiveState() string {
	out, _ := exec.Command("systemctl", "is-active", UnitName).Output()
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return "unknown"
}
