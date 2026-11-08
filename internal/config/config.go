// Package config loads and validates monitoring configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration so configuration can use values such as "5s",
// "15s", "1h", and "90d". Go's time.ParseDuration does not accept days.
type Duration time.Duration

// D returns the duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Seconds returns the duration in whole seconds.
func (d Duration) Seconds() int64 { return int64(time.Duration(d) / time.Second) }

func (d Duration) String() string {
	dd := time.Duration(d)
	if dd > 0 && dd%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(dd/(24*time.Hour)))
	}
	if dd > 0 && dd%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(dd/time.Hour))
	}
	if dd > 0 && dd%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(dd/time.Minute))
	}
	return dd.String()
}

// ParseDuration parses a monitoring duration. Bare numbers are seconds.
// A trailing "d" means 24-hour days.
func ParseDuration(in string) (Duration, error) {
	txt := strings.TrimSpace(strings.ToLower(in))
	if txt == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(txt, "d") {
		n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(txt, "d")), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", in, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("invalid duration %q: must not be negative", in)
		}
		return Duration(n * float64(24*time.Hour)), nil
	}
	parsed, err := time.ParseDuration(txt)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", in, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("invalid duration %q: must not be negative", in)
	}
	return Duration(parsed), nil
}

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		parsed, err := ParseDuration(v)
		if err != nil {
			return err
		}
		*d = parsed
	case int:
		*d = Duration(time.Duration(v) * time.Second)
	case int64:
		*d = Duration(time.Duration(v) * time.Second)
	case float64:
		*d = Duration(time.Duration(v * float64(time.Second)))
	default:
		return fmt.Errorf("invalid duration value %v (%T)", raw, raw)
	}
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

// Size is a byte count written as "1GB", "512MiB", or a plain number of bytes.
type Size int64

// Bytes returns the size in bytes.
func (s Size) Bytes() int64 { return int64(s) }

var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"kib", 1 << 10},
	{"mib", 1 << 20},
	{"gib", 1 << 30},
	{"tib", 1 << 40},
	{"kb", 1000},
	{"mb", 1000 * 1000},
	{"gb", 1000 * 1000 * 1000},
	{"tb", 1000 * 1000 * 1000 * 1000},
	{"k", 1 << 10},
	{"m", 1 << 20},
	{"g", 1 << 30},
	{"t", 1 << 40},
	{"b", 1},
}

// ParseSize converts a human byte size into a byte count.
// Decimal units (KB, MB, GB) are powers of 1000. Binary units (KiB, MiB, GiB)
// are powers of 1024. "1GB" is therefore 1000000000 bytes.
func ParseSize(in string) (Size, error) {
	txt := strings.ToLower(strings.TrimSpace(in))
	if txt == "" {
		return 0, fmt.Errorf("empty size")
	}
	for _, u := range sizeUnits {
		if strings.HasSuffix(txt, u.suffix) {
			numPart := strings.TrimSpace(strings.TrimSuffix(txt, u.suffix))
			n, err := strconv.ParseFloat(numPart, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q: %w", in, err)
			}
			if n < 0 {
				return 0, fmt.Errorf("invalid size %q: must not be negative", in)
			}
			return Size(n * float64(u.mult)), nil
		}
	}
	n, err := strconv.ParseFloat(txt, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", in, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid size %q: must not be negative", in)
	}
	return Size(n), nil
}

func (s *Size) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		parsed, err := ParseSize(v)
		if err != nil {
			return err
		}
		*s = parsed
	case int:
		*s = Size(v)
	case int64:
		*s = Size(v)
	case float64:
		*s = Size(v)
	default:
		return fmt.Errorf("invalid size value %v (%T)", raw, raw)
	}
	return nil
}

func (s Size) MarshalYAML() (any, error) { return s.String(), nil }

func (s Size) String() string {
	n := int64(s)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"GB", 1000 * 1000 * 1000}, {"MB", 1000 * 1000}, {"KB", 1000},
	} {
		if n >= u.mult && n%u.mult == 0 {
			return fmt.Sprintf("%d%s", n/u.mult, u.suffix)
		}
	}
	return strconv.FormatInt(n, 10)
}

// Sampling intervals. Sub-second polling is rejected.
type Sampling struct {
	HostInterval       Duration `yaml:"host_interval"`
	ProcessInterval    Duration `yaml:"process_interval"`
	FilesystemInterval Duration `yaml:"filesystem_interval"`
}

// Retention windows, measured from now. Each tier is kept until the next
// boundary, then rolled into a coarser resolution.
type Retention struct {
	Raw     Duration `yaml:"raw"`
	Short   Duration `yaml:"short"`
	Medium  Duration `yaml:"medium"`
	Long    Duration `yaml:"long"`
	Maximum Duration `yaml:"maximum"`
}

// Rollups are the bucket widths used once data ages out of a finer tier.
type Rollups struct {
	ShortInterval   Duration `yaml:"short_interval"`
	MediumInterval  Duration `yaml:"medium_interval"`
	LongInterval    Duration `yaml:"long_interval"`
	ArchiveInterval Duration `yaml:"archive_interval"`
}

// Storage is the on-disk budget. It takes priority over time-based retention.
type Storage struct {
	MaxSize Size `yaml:"max_size"`
}

// Process controls process-sample capture.
type Process struct {
	CommandLineMaxBytes int `yaml:"command_line_max_bytes"`
}

// Filesystems controls capacity collection.
type Filesystems struct {
	Timeout Duration `yaml:"timeout"`
	// Exclude drops mounts whose mount point, device, or filesystem type
	// matches a filepath.Match pattern. Excluded mounts are never statfs'd.
	Exclude []string `yaml:"exclude,omitempty"`
}

// Config is the monitoring configuration file.
type Config struct {
	Sampling    Sampling    `yaml:"sampling"`
	Retention   Retention   `yaml:"retention"`
	Rollups     Rollups     `yaml:"rollups"`
	Storage     Storage     `yaml:"storage"`
	Process     Process     `yaml:"process"`
	Filesystems Filesystems `yaml:"filesystems"`
}

const (
	minInterval     = time.Second
	maxInterval     = 24 * time.Hour
	minCommandBytes = 64
	maxCommandBytes = 64 * 1024
	minStorage      = 256 * 1024
	maxStorage      = 10 * 1000 * 1000 * 1000 * 1000 // 10 TB
	maxRetention    = 10 * 365 * 24 * time.Hour
)

// Default returns the v1 monitoring profile.
func Default() *Config {
	return &Config{
		Sampling: Sampling{
			HostInterval:       Duration(5 * time.Second),
			ProcessInterval:    Duration(5 * time.Second),
			FilesystemInterval: Duration(60 * time.Second),
		},
		Retention: Retention{
			Raw:     Duration(time.Hour),
			Short:   Duration(6 * time.Hour),
			Medium:  Duration(24 * time.Hour),
			Long:    Duration(7 * 24 * time.Hour),
			Maximum: Duration(90 * 24 * time.Hour),
		},
		Rollups: Rollups{
			ShortInterval:   Duration(15 * time.Second),
			MediumInterval:  Duration(time.Minute),
			LongInterval:    Duration(5 * time.Minute),
			ArchiveInterval: Duration(time.Hour),
		},
		Storage:     Storage{MaxSize: 1000 * 1000 * 1000},
		Process:     Process{CommandLineMaxBytes: 4096},
		Filesystems: Filesystems{Timeout: Duration(10 * time.Second)},
	}
}

// ApplyDefaults fills zero fields from Default. Explicit values are kept.
func (c *Config) ApplyDefaults() {
	d := Default()
	if c.Sampling.HostInterval == 0 {
		c.Sampling.HostInterval = d.Sampling.HostInterval
	}
	if c.Sampling.ProcessInterval == 0 {
		c.Sampling.ProcessInterval = d.Sampling.ProcessInterval
	}
	if c.Sampling.FilesystemInterval == 0 {
		c.Sampling.FilesystemInterval = d.Sampling.FilesystemInterval
	}
	if c.Retention.Raw == 0 {
		c.Retention.Raw = d.Retention.Raw
	}
	if c.Retention.Short == 0 {
		c.Retention.Short = d.Retention.Short
	}
	if c.Retention.Medium == 0 {
		c.Retention.Medium = d.Retention.Medium
	}
	if c.Retention.Long == 0 {
		c.Retention.Long = d.Retention.Long
	}
	if c.Retention.Maximum == 0 {
		c.Retention.Maximum = d.Retention.Maximum
	}
	if c.Rollups.ShortInterval == 0 {
		c.Rollups.ShortInterval = d.Rollups.ShortInterval
	}
	if c.Rollups.MediumInterval == 0 {
		c.Rollups.MediumInterval = d.Rollups.MediumInterval
	}
	if c.Rollups.LongInterval == 0 {
		c.Rollups.LongInterval = d.Rollups.LongInterval
	}
	if c.Rollups.ArchiveInterval == 0 {
		c.Rollups.ArchiveInterval = d.Rollups.ArchiveInterval
	}
	if c.Storage.MaxSize == 0 {
		c.Storage.MaxSize = d.Storage.MaxSize
	}
	if c.Process.CommandLineMaxBytes == 0 {
		c.Process.CommandLineMaxBytes = d.Process.CommandLineMaxBytes
	}
	if c.Filesystems.Timeout == 0 {
		c.Filesystems.Timeout = d.Filesystems.Timeout
	}
}

// Validate rejects intervals, tier orderings, and sizes that would make
// collection unsafe or rollups ambiguous.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	host := c.Sampling.HostInterval.D()
	proc := c.Sampling.ProcessInterval.D()
	fs := c.Sampling.FilesystemInterval.D()
	if err := checkInterval("sampling.host_interval", host); err != nil {
		return err
	}
	if err := checkInterval("sampling.process_interval", proc); err != nil {
		return err
	}
	if err := checkInterval("sampling.filesystem_interval", fs); err != nil {
		return err
	}

	short := c.Rollups.ShortInterval.D()
	medium := c.Rollups.MediumInterval.D()
	long := c.Rollups.LongInterval.D()
	archive := c.Rollups.ArchiveInterval.D()
	for _, item := range []struct {
		n string
		d time.Duration
	}{
		{"rollups.short_interval", short},
		{"rollups.medium_interval", medium},
		{"rollups.long_interval", long},
		{"rollups.archive_interval", archive},
	} {
		if err := checkInterval(item.n, item.d); err != nil {
			return err
		}
	}
	if short <= host {
		return fmt.Errorf("rollups.short_interval (%s) must be coarser than sampling.host_interval (%s)", short, host)
	}
	if medium <= short || long <= medium || archive <= long {
		return fmt.Errorf("rollup intervals must increase: short < medium < long < archive")
	}
	if err := multiple("rollups.short_interval", short, host); err != nil {
		return err
	}
	if err := multiple("rollups.medium_interval", medium, short); err != nil {
		return err
	}
	if err := multiple("rollups.long_interval", long, medium); err != nil {
		return err
	}
	if err := multiple("rollups.archive_interval", archive, long); err != nil {
		return err
	}

	raw := c.Retention.Raw.D()
	retShort := c.Retention.Short.D()
	retMed := c.Retention.Medium.D()
	retLong := c.Retention.Long.D()
	retMax := c.Retention.Maximum.D()
	windows := []struct {
		n string
		d time.Duration
	}{
		{"retention.raw", raw},
		{"retention.short", retShort},
		{"retention.medium", retMed},
		{"retention.long", retLong},
		{"retention.maximum", retMax},
	}
	for _, w := range windows {
		if w.d < time.Minute {
			return fmt.Errorf("%s must be at least 1m", w.n)
		}
		if w.d > maxRetention {
			return fmt.Errorf("%s exceeds the 10 year limit", w.n)
		}
		if w.d%time.Second != 0 {
			return fmt.Errorf("%s must be a whole number of seconds", w.n)
		}
	}
	if !(raw < retShort && retShort < retMed && retMed < retLong && retLong < retMax) {
		return fmt.Errorf("retention windows must increase: raw < short < medium < long < maximum")
	}
	if raw <= host {
		return fmt.Errorf("retention.raw must be longer than sampling.host_interval")
	}
	if archive >= retMax {
		return fmt.Errorf("rollups.archive_interval must be shorter than retention.maximum")
	}

	if c.Storage.MaxSize < minStorage || int64(c.Storage.MaxSize) > maxStorage {
		return fmt.Errorf("storage.max_size must be between %d bytes and %d bytes", minStorage, maxStorage)
	}
	if c.Process.CommandLineMaxBytes < minCommandBytes || c.Process.CommandLineMaxBytes > maxCommandBytes {
		return fmt.Errorf("process.command_line_max_bytes must be between %d and %d", minCommandBytes, maxCommandBytes)
	}
	if c.Filesystems.Timeout.D() < time.Second || c.Filesystems.Timeout.D() > time.Minute {
		return fmt.Errorf("filesystems.timeout must be between 1s and 1m")
	}
	for _, pat := range c.Filesystems.Exclude {
		if strings.ContainsAny(pat, "\n\r") {
			return fmt.Errorf("filesystems.exclude pattern %q contains a newline", pat)
		}
		if _, err := filepath.Match(pat, "x"); err != nil {
			return fmt.Errorf("filesystems.exclude pattern %q: %w", pat, err)
		}
	}
	return nil
}

func checkInterval(name string, d time.Duration) error {
	if d < minInterval {
		return fmt.Errorf("%s must be at least 1s (sub-second polling is not allowed)", name)
	}
	if d > maxInterval {
		return fmt.Errorf("%s must be at most 24h", name)
	}
	if d%time.Second != 0 {
		return fmt.Errorf("%s must be a whole number of seconds", name)
	}
	return nil
}

func multiple(name string, d, of time.Duration) error {
	if of <= 0 || d%of != 0 {
		return fmt.Errorf("%s (%s) must be a multiple of %s", name, d, of)
	}
	return nil
}

// Load reads path, applies defaults, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// Save writes cfg atomically as YAML with mode 0600.
func Save(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshaling: %w", err)
	}
	return writeAtomic(path, data, 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: creating directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("config: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	return os.Chmod(path, mode)
}
