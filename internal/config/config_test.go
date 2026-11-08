package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseDurationAndSize(t *testing.T) {
	d, err := ParseDuration("90d")
	if err != nil || d.D() != 90*24*time.Hour {
		t.Fatalf("90d = %v %v", d, err)
	}
	d, err = ParseDuration("7d")
	if err != nil || d.Seconds() != 7*24*3600 {
		t.Fatalf("7d = %v %v", d, err)
	}
	s, err := ParseSize("1GB")
	if err != nil || s.Bytes() != 1000*1000*1000 {
		t.Fatalf("1GB = %d %v", s, err)
	}
	s, err = ParseSize("1GiB")
	if err != nil || s.Bytes() != 1<<30 {
		t.Fatalf("1GiB = %d %v", s, err)
	}
}

func TestRejectsUnsafeConfig(t *testing.T) {
	cfg := Default()
	cfg.Sampling.HostInterval = Duration(500 * time.Millisecond)
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sub-second") {
		t.Fatalf("500ms: %v", err)
	}

	cfg = Default()
	cfg.Retention.Short = cfg.Retention.Raw
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "retention windows must increase") {
		t.Fatalf("tier order: %v", err)
	}

	cfg = Default()
	cfg.Rollups.MediumInterval = Duration(20 * time.Second)
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("non-multiple rollup: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	cfg := Default()
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sampling.HostInterval != cfg.Sampling.HostInterval || got.Retention.Maximum.Seconds() != 90*24*3600 {
		t.Fatalf("round trip = %+v", got)
	}
}
