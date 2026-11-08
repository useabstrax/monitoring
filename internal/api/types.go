// Package api is the public monitoring response schema.
//
// schema_version is independent of the plugin version and of the SQLite
// user_version. Callers cache and interpret these structs. They never see
// table layout.
package api

// SchemaVersion is the public monitoring response schema.
const SchemaVersion = 1

// LiveTopN is how many processes a live sample includes, ranked by CPU.
const LiveTopN = 15

// DefaultMaxPoints is the history page size when the caller omits --max-points.
const DefaultMaxPoints = 240

// HardMaxPoints is the upper bound accepted for --max-points.
const HardMaxPoints = 500

// MaxPayloadBytes keeps a history page under the agent's ~1 MiB result cap.
const MaxPayloadBytes = 768 * 1024

// Snapshot is the cheap latest.json document future heartbeats can read.
type Snapshot struct {
	SchemaVersion           int    `json:"schema_version"`
	CollectedAt             string `json:"collected_at"`
	ExpectedIntervalSeconds int    `json:"expected_interval_seconds"`
	Vitals                  Vitals `json:"vitals"`
}

// Vitals is the small current-health summary.
type Vitals struct {
	CPUUsagePct     float64 `json:"cpu_usage_pct"`
	MemoryUsagePct  float64 `json:"memory_usage_pct"`
	DiskUsagePctMax float64 `json:"disk_usage_pct_max"`
	DiskUsageMount  string  `json:"disk_usage_mount"`
	Load1m          float64 `json:"load_1m"`
	CPUCores        int     `json:"cpu_cores"`
}

// HostLatest is the newest host sample, in display units.
type HostLatest struct {
	CPUUsagePct                float64 `json:"cpu_usage_pct"`
	CPUUserPct                 float64 `json:"cpu_user_pct"`
	CPUSystemPct               float64 `json:"cpu_system_pct"`
	CPUIOWaitPct               float64 `json:"cpu_iowait_pct"`
	CPUStealPct                float64 `json:"cpu_steal_pct"`
	CPUCores                   int     `json:"cpu_cores"`
	MemoryTotalBytes           int64   `json:"memory_total_bytes"`
	MemoryUsedBytes            int64   `json:"memory_used_bytes"`
	MemoryAvailableBytes       int64   `json:"memory_available_bytes"`
	MemoryUsagePct             float64 `json:"memory_usage_pct"`
	SwapTotalBytes             int64   `json:"swap_total_bytes"`
	SwapUsedBytes              int64   `json:"swap_used_bytes"`
	SwapUsagePct               float64 `json:"swap_usage_pct"`
	Load1m                     float64 `json:"load_1m"`
	Load5m                     float64 `json:"load_5m"`
	Load15m                    float64 `json:"load_15m"`
	UptimeSeconds              int64   `json:"uptime_seconds"`
	DiskReadBytesPerSec        float64 `json:"disk_read_bytes_per_sec"`
	DiskWriteBytesPerSec       float64 `json:"disk_write_bytes_per_sec"`
	NetworkReceiveBytesPerSec  float64 `json:"network_receive_bytes_per_sec"`
	NetworkTransmitBytesPerSec float64 `json:"network_transmit_bytes_per_sec"`
}

// FilesystemLatest is one mount's current capacity.
type FilesystemLatest struct {
	Mount          string  `json:"mount"`
	Device         string  `json:"device"`
	FilesystemType string  `json:"filesystem_type"`
	TotalBytes     int64   `json:"total_bytes"`
	UsedBytes      int64   `json:"used_bytes"`
	AvailableBytes int64   `json:"available_bytes"`
	UsagePct       float64 `json:"usage_pct"`
	InodeTotal     int64   `json:"inode_total"`
	InodeUsed      int64   `json:"inode_used"`
	InodeUsagePct  float64 `json:"inode_usage_pct"`
}

// Process is one process instance, current or historical.
type Process struct {
	InstanceID           string  `json:"instance_id"`
	PID                  int     `json:"pid"`
	PPID                 int     `json:"ppid"`
	BootID               string  `json:"boot_id,omitempty"`
	StarttimeTicks       int64   `json:"starttime_ticks,omitempty"`
	StartedAt            string  `json:"started_at,omitempty"`
	Name                 string  `json:"name"`
	Executable           string  `json:"executable,omitempty"`
	CommandLine          string  `json:"command_line"`
	CommandLineTruncated bool    `json:"command_line_truncated"`
	User                 string  `json:"user,omitempty"`
	Kernel               bool    `json:"kernel,omitempty"`
	Exited               bool    `json:"exited,omitempty"`
	CPUUsagePct          float64 `json:"cpu_usage_pct"`
	CPUUsagePctMax       float64 `json:"cpu_usage_pct_max,omitempty"`
	RSSBytes             int64   `json:"rss_bytes"`
	MemoryUsagePct       float64 `json:"memory_usage_pct"`
	ReadBytesPerSec      float64 `json:"read_bytes_per_sec"`
	WriteBytesPerSec     float64 `json:"write_bytes_per_sec"`
	ThreadCount          int     `json:"thread_count"`
	State                string  `json:"state,omitempty"`
	SampleCount          int     `json:"sample_count,omitempty"`
	LastSeen             string  `json:"last_seen,omitempty"`
}

// Latest is the newest monitoring state.
type Latest struct {
	SchemaVersion int                `json:"schema_version"`
	CollectedAt   string             `json:"collected_at,omitempty"`
	Warm          bool               `json:"warm"`
	Host          HostLatest         `json:"host"`
	Filesystems   []FilesystemLatest `json:"filesystems"`
	ProcessCount  int                `json:"process_count"`
	TopProcesses  []Process          `json:"top_processes"`
}

// LiveSample is one NDJSON event from `monitoring live`.
// Collection persists the same host sample before it is emitted.
type LiveSample struct {
	Type          string             `json:"type"`
	SchemaVersion int                `json:"schema_version"`
	Timestamp     string             `json:"timestamp"`
	Host          HostLatest         `json:"host"`
	Filesystems   []FilesystemLatest `json:"filesystems"`
	ProcessCount  int                `json:"process_count"`
	TopProcesses  []Process          `json:"top_processes"`
}

// History is one coherent host and filesystem page.
type History struct {
	SchemaVersion     int                `json:"schema_version"`
	From              string             `json:"from"`
	To                string             `json:"to"`
	ResolutionSeconds int                `json:"resolution_seconds"`
	Tier              string             `json:"tier"`
	MaxPoints         int                `json:"max_points"`
	AvailableFrom     string             `json:"available_from,omitempty"`
	AvailableTo       string             `json:"available_to,omitempty"`
	Host              []HostPoint        `json:"host"`
	Filesystems       []FilesystemSeries `json:"filesystems"`
	NextCursor        *string            `json:"next_cursor"`
}

// HostPoint is one bucket of host history. Averages and maxima are both kept
// so a short spike is not erased by the bucket average.
type HostPoint struct {
	Timestamp                     string  `json:"timestamp"`
	SampleCount                   int     `json:"sample_count"`
	CPUUsagePctAvg                float64 `json:"cpu_usage_pct_avg"`
	CPUUsagePctMax                float64 `json:"cpu_usage_pct_max"`
	CPUUserPctAvg                 float64 `json:"cpu_user_pct_avg"`
	CPUUserPctMax                 float64 `json:"cpu_user_pct_max"`
	CPUSystemPctAvg               float64 `json:"cpu_system_pct_avg"`
	CPUSystemPctMax               float64 `json:"cpu_system_pct_max"`
	CPUIOWaitPctAvg               float64 `json:"cpu_iowait_pct_avg"`
	CPUIOWaitPctMax               float64 `json:"cpu_iowait_pct_max"`
	CPUStealPctAvg                float64 `json:"cpu_steal_pct_avg"`
	CPUStealPctMax                float64 `json:"cpu_steal_pct_max"`
	CPUCores                      int     `json:"cpu_cores"`
	MemoryTotalBytes              int64   `json:"memory_total_bytes"`
	MemoryUsedBytesAvg            int64   `json:"memory_used_bytes_avg"`
	MemoryUsedBytesMax            int64   `json:"memory_used_bytes_max"`
	MemoryAvailableBytesAvg       int64   `json:"memory_available_bytes_avg"`
	MemoryUsagePctAvg             float64 `json:"memory_usage_pct_avg"`
	MemoryUsagePctMax             float64 `json:"memory_usage_pct_max"`
	SwapTotalBytes                int64   `json:"swap_total_bytes"`
	SwapUsedBytesAvg              int64   `json:"swap_used_bytes_avg"`
	SwapUsedBytesMax              int64   `json:"swap_used_bytes_max"`
	SwapUsagePctAvg               float64 `json:"swap_usage_pct_avg"`
	SwapUsagePctMax               float64 `json:"swap_usage_pct_max"`
	Load1mAvg                     float64 `json:"load_1m_avg"`
	Load1mMax                     float64 `json:"load_1m_max"`
	Load5mAvg                     float64 `json:"load_5m_avg"`
	Load5mMax                     float64 `json:"load_5m_max"`
	Load15mAvg                    float64 `json:"load_15m_avg"`
	Load15mMax                    float64 `json:"load_15m_max"`
	UptimeSeconds                 int64   `json:"uptime_seconds"`
	DiskReadBytesPerSecAvg        float64 `json:"disk_read_bytes_per_sec_avg"`
	DiskReadBytesPerSecMax        float64 `json:"disk_read_bytes_per_sec_max"`
	DiskWriteBytesPerSecAvg       float64 `json:"disk_write_bytes_per_sec_avg"`
	DiskWriteBytesPerSecMax       float64 `json:"disk_write_bytes_per_sec_max"`
	NetworkReceiveBytesPerSecAvg  float64 `json:"network_receive_bytes_per_sec_avg"`
	NetworkReceiveBytesPerSecMax  float64 `json:"network_receive_bytes_per_sec_max"`
	NetworkTransmitBytesPerSecAvg float64 `json:"network_transmit_bytes_per_sec_avg"`
	NetworkTransmitBytesPerSecMax float64 `json:"network_transmit_bytes_per_sec_max"`
}

// FilesystemSeries is one mount across the page window.
type FilesystemSeries struct {
	Mount          string            `json:"mount"`
	Device         string            `json:"device"`
	FilesystemType string            `json:"filesystem_type"`
	Points         []FilesystemPoint `json:"points"`
}

// FilesystemPoint is one bucket of filesystem capacity.
type FilesystemPoint struct {
	Timestamp         string  `json:"timestamp"`
	SampleCount       int     `json:"sample_count"`
	TotalBytes        int64   `json:"total_bytes"`
	UsedBytesAvg      int64   `json:"used_bytes_avg"`
	UsedBytesMax      int64   `json:"used_bytes_max"`
	AvailableBytesAvg int64   `json:"available_bytes_avg"`
	UsagePctAvg       float64 `json:"usage_pct_avg"`
	UsagePctMax       float64 `json:"usage_pct_max"`
	InodeTotal        int64   `json:"inode_total"`
	InodeUsedAvg      int64   `json:"inode_used_avg"`
	InodeUsedMax      int64   `json:"inode_used_max"`
	InodeUsagePctAvg  float64 `json:"inode_usage_pct_avg"`
	InodeUsagePctMax  float64 `json:"inode_usage_pct_max"`
}

// ProcessList is the current or historical process listing.
type ProcessList struct {
	SchemaVersion int       `json:"schema_version"`
	CollectedAt   string    `json:"collected_at,omitempty"`
	At            string    `json:"at,omitempty"`
	RequestedAt   string    `json:"requested_at,omitempty"`
	Sort          string    `json:"sort"`
	Processes     []Process `json:"processes"`
}

// ProcessHistory is one process instance's history page.
type ProcessHistory struct {
	SchemaVersion     int            `json:"schema_version"`
	InstanceID        string         `json:"instance_id"`
	From              string         `json:"from"`
	To                string         `json:"to"`
	ResolutionSeconds int            `json:"resolution_seconds"`
	Tier              string         `json:"tier"`
	MaxPoints         int            `json:"max_points"`
	AvailableFrom     string         `json:"available_from,omitempty"`
	AvailableTo       string         `json:"available_to,omitempty"`
	Points            []ProcessPoint `json:"points"`
	NextCursor        *string        `json:"next_cursor"`
}

// ProcessPoint is one bucket of a single process.
type ProcessPoint struct {
	Timestamp           string  `json:"timestamp"`
	SampleCount         int     `json:"sample_count"`
	CPUUsagePctAvg      float64 `json:"cpu_usage_pct_avg"`
	CPUUsagePctMax      float64 `json:"cpu_usage_pct_max"`
	RSSBytesAvg         int64   `json:"rss_bytes_avg"`
	RSSBytesMax         int64   `json:"rss_bytes_max"`
	MemoryUsagePctAvg   float64 `json:"memory_usage_pct_avg"`
	MemoryUsagePctMax   float64 `json:"memory_usage_pct_max"`
	ReadBytesPerSecAvg  float64 `json:"read_bytes_per_sec_avg"`
	ReadBytesPerSecMax  float64 `json:"read_bytes_per_sec_max"`
	WriteBytesPerSecAvg float64 `json:"write_bytes_per_sec_avg"`
	WriteBytesPerSecMax float64 `json:"write_bytes_per_sec_max"`
	ThreadCountAvg      int     `json:"thread_count_avg"`
	ThreadCountMax      int     `json:"thread_count_max"`
	State               string  `json:"state,omitempty"`
}

// Status describes the monitoring daemon, not the current metric sample.
type Status struct {
	SchemaVersion              int    `json:"schema_version"`
	DaemonState                string `json:"daemon_state"`
	Version                    string `json:"version"`
	Collecting                 bool   `json:"collecting"`
	LatestSampleAt             string `json:"latest_sample_at,omitempty"`
	OldestSampleAt             string `json:"oldest_sample_at,omitempty"`
	HostIntervalSeconds        int    `json:"host_interval_seconds"`
	ProcessIntervalSeconds     int    `json:"process_interval_seconds"`
	FilesystemIntervalSeconds  int    `json:"filesystem_interval_seconds"`
	ConfiguredRetentionSeconds int64  `json:"configured_retention_seconds"`
	EffectiveRetentionSeconds  int64  `json:"effective_retention_seconds"`
	StorageConstrained         bool   `json:"storage_constrained"`
	DatabaseBytes              int64  `json:"database_bytes"`
	MaxStorageBytes            int64  `json:"max_storage_bytes"`
	ProcessInstances           int64  `json:"process_instances"`
	UptimeSeconds              int64  `json:"uptime_seconds"`
}

// StorageStatus is the on-disk budget report.
type StorageStatus struct {
	SchemaVersion              int        `json:"schema_version"`
	DatabaseBytes              int64      `json:"database_bytes"`
	MaxStorageBytes            int64      `json:"max_storage_bytes"`
	PercentUsed                float64    `json:"percent_used"`
	OldestSampleAt             string     `json:"oldest_sample_at,omitempty"`
	NewestSampleAt             string     `json:"newest_sample_at,omitempty"`
	ConfiguredRetentionSeconds int64      `json:"configured_retention_seconds"`
	EffectiveRetentionSeconds  int64      `json:"effective_retention_seconds"`
	StorageConstrained         bool       `json:"storage_constrained"`
	Tiers                      []TierInfo `json:"tiers"`
}

// TierInfo describes one retention tier.
type TierInfo struct {
	Name              string `json:"name"`
	ResolutionSeconds int64  `json:"resolution_seconds"`
	RetainSeconds     int64  `json:"retain_seconds"`
}

// ConfigView is the configuration as integer seconds and bytes.
type ConfigView struct {
	SchemaVersion             int      `json:"schema_version"`
	HostIntervalSeconds       int      `json:"host_interval_seconds"`
	ProcessIntervalSeconds    int      `json:"process_interval_seconds"`
	FilesystemIntervalSeconds int      `json:"filesystem_interval_seconds"`
	RetentionRawSeconds       int64    `json:"retention_raw_seconds"`
	RetentionShortSeconds     int64    `json:"retention_short_seconds"`
	RetentionMediumSeconds    int64    `json:"retention_medium_seconds"`
	RetentionLongSeconds      int64    `json:"retention_long_seconds"`
	RetentionMaximumSeconds   int64    `json:"retention_maximum_seconds"`
	RollupShortSeconds        int64    `json:"rollup_short_seconds"`
	RollupMediumSeconds       int64    `json:"rollup_medium_seconds"`
	RollupLongSeconds         int64    `json:"rollup_long_seconds"`
	RollupArchiveSeconds      int64    `json:"rollup_archive_seconds"`
	MaxStorageBytes           int64    `json:"max_storage_bytes"`
	CommandLineMaxBytes       int      `json:"command_line_max_bytes"`
	FilesystemTimeoutSeconds  int      `json:"filesystem_timeout_seconds"`
	FilesystemExclude         []string `json:"filesystem_exclude"`
}
