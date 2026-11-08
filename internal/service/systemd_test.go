package service

import (
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	unit, err := RenderUnit(UnitOptions{
		Binary:     "/usr/local/lib/abstrax/plugins/abstrax-monitoring",
		ConfigFile: "/etc/abstrax/plugins/monitoring/config.yaml",
		StateDir:   "/var/lib/abstrax/plugins/monitoring",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"User=root",
		"Group=root",
		"Restart=on-failure",
		"RestartPreventExitStatus=78",
		"TimeoutStopSec=30",
		"StandardOutput=journal",
		"StandardError=journal",
		"SyslogIdentifier=abstrax-monitoring",
		"ABSTRAX_MONITORING_CONFIG=/etc/abstrax/plugins/monitoring/config.yaml",
		"ABSTRAX_MONITORING_STATE_DIR=/var/lib/abstrax/plugins/monitoring",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %s\n%s", want, unit)
		}
	}
	if _, err := RenderUnit(UnitOptions{Binary: "abstrax-monitoring", ConfigFile: "/etc/c", StateDir: "/var/lib"}); err == nil {
		t.Fatal("relative binary was accepted")
	}
}
