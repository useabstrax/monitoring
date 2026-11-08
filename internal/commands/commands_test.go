package commands

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/plugin"
)

func TestMetadataAndVersion(t *testing.T) {
	bin := build(t)
	out, err := exec.Command(bin, "plugin", "metadata").Output()
	if err != nil {
		t.Fatal(err)
	}
	var meta plugin.Metadata
	if err := json.Unmarshal(out, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ProtocolVersion != 1 || meta.Name != "monitoring" {
		t.Fatalf("metadata = %+v", meta)
	}
	var live, others int
	for _, c := range meta.Commands {
		if c.Streamable {
			live++
			if c.Action != "plugin.monitoring.live" {
				t.Fatalf("unexpected streamable command %+v", c)
			}
		} else {
			others++
		}
		if c.Action == "" || strings.Contains(c.Action, " ") {
			t.Fatalf("action = %q", c.Action)
		}
	}
	if live != 1 || others < 5 {
		t.Fatalf("streamable=%d other=%d", live, others)
	}
	version, err := exec.Command(bin, "version").Output()
	if err != nil || !strings.Contains(string(version), "Plugin version:") {
		t.Fatalf("version: %v %s", err, version)
	}
}

func TestConfigValidation(t *testing.T) {
	bin := build(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	state := filepath.Join(dir, "state")
	cmd := exec.Command(bin, "--config", cfg, "--state-dir", state, "config", "--host-interval", "500ms")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "sub-second") {
		t.Fatalf("500ms err=%v out=%s", err, out)
	}
}

func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "abstrax-monitoring")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/abstrax-monitoring")
	cmd.Dir = moduleRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
