package store

// ResRaw marks samples at the collector's native interval. Rollup tiers use
// their bucket width in seconds. A timestamp is stored at one resolution:
// rolling up inserts the coarser rows and deletes the finer rows in the same
// transaction.
const ResRaw int64 = 0

const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS host_samples (
  res INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  sample_count INTEGER NOT NULL,
  cpu_usage_sum REAL NOT NULL,
  cpu_usage_max REAL NOT NULL,
  cpu_user_sum REAL NOT NULL,
  cpu_user_max REAL NOT NULL,
  cpu_system_sum REAL NOT NULL,
  cpu_system_max REAL NOT NULL,
  cpu_iowait_sum REAL NOT NULL,
  cpu_iowait_max REAL NOT NULL,
  cpu_steal_sum REAL NOT NULL,
  cpu_steal_max REAL NOT NULL,
  cpu_cores INTEGER NOT NULL,
  mem_total INTEGER NOT NULL,
  mem_used_sum INTEGER NOT NULL,
  mem_used_max INTEGER NOT NULL,
  mem_avail_sum INTEGER NOT NULL,
  mem_usage_sum REAL NOT NULL,
  mem_usage_max REAL NOT NULL,
  swap_total INTEGER NOT NULL,
  swap_used_sum INTEGER NOT NULL,
  swap_used_max INTEGER NOT NULL,
  swap_usage_sum REAL NOT NULL,
  swap_usage_max REAL NOT NULL,
  load1_sum REAL NOT NULL,
  load1_max REAL NOT NULL,
  load5_sum REAL NOT NULL,
  load5_max REAL NOT NULL,
  load15_sum REAL NOT NULL,
  load15_max REAL NOT NULL,
  uptime_max INTEGER NOT NULL,
  disk_read_bytes INTEGER NOT NULL,
  disk_read_ms INTEGER NOT NULL,
  disk_read_bps_max REAL NOT NULL,
  disk_write_bytes INTEGER NOT NULL,
  disk_write_ms INTEGER NOT NULL,
  disk_write_bps_max REAL NOT NULL,
  net_rx_bytes INTEGER NOT NULL,
  net_rx_ms INTEGER NOT NULL,
  net_rx_bps_max REAL NOT NULL,
  net_tx_bytes INTEGER NOT NULL,
  net_tx_ms INTEGER NOT NULL,
  net_tx_bps_max REAL NOT NULL,
  PRIMARY KEY (res, ts)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS filesystem_samples (
  res INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  mount TEXT NOT NULL,
  device TEXT NOT NULL,
  fstype TEXT NOT NULL,
  sample_count INTEGER NOT NULL,
  total_bytes INTEGER NOT NULL,
  used_sum INTEGER NOT NULL,
  used_max INTEGER NOT NULL,
  avail_sum INTEGER NOT NULL,
  usage_sum REAL NOT NULL,
  usage_max REAL NOT NULL,
  inode_total INTEGER NOT NULL,
  inode_used_sum INTEGER NOT NULL,
  inode_used_max INTEGER NOT NULL,
  inode_usage_sum REAL NOT NULL,
  inode_usage_max REAL NOT NULL,
  PRIMARY KEY (res, ts, mount)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS process_instances (
  instance_id TEXT PRIMARY KEY,
  boot_id TEXT NOT NULL,
  pid INTEGER NOT NULL,
  starttime_ticks INTEGER NOT NULL,
  ppid INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  name TEXT NOT NULL,
  executable TEXT NOT NULL,
  command_line TEXT NOT NULL,
  command_line_truncated INTEGER NOT NULL,
  user_name TEXT NOT NULL,
  uid INTEGER NOT NULL,
  kernel INTEGER NOT NULL,
  first_seen INTEGER NOT NULL,
  last_seen INTEGER NOT NULL,
  exited INTEGER NOT NULL,
  UNIQUE (boot_id, pid, starttime_ticks)
);

CREATE INDEX IF NOT EXISTS process_instances_last ON process_instances(last_seen);

CREATE TABLE IF NOT EXISTS process_samples (
  res INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  instance_id TEXT NOT NULL,
  sample_count INTEGER NOT NULL,
  cpu_sum REAL NOT NULL,
  cpu_max REAL NOT NULL,
  rss_sum INTEGER NOT NULL,
  rss_max INTEGER NOT NULL,
  mem_sum REAL NOT NULL,
  mem_max REAL NOT NULL,
  read_bytes INTEGER NOT NULL,
  read_ms INTEGER NOT NULL,
  read_bps_max REAL NOT NULL,
  write_bytes INTEGER NOT NULL,
  write_ms INTEGER NOT NULL,
  write_bps_max REAL NOT NULL,
  threads_sum INTEGER NOT NULL,
  threads_max INTEGER NOT NULL,
  state TEXT NOT NULL,
  PRIMARY KEY (res, ts, instance_id)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS process_samples_inst ON process_samples(instance_id, res, ts);
`
