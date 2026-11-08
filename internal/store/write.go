package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/collect"
)

const insertHost = `
INSERT INTO host_samples (
  res, ts, sample_count,
  cpu_usage_sum, cpu_usage_max, cpu_user_sum, cpu_user_max,
  cpu_system_sum, cpu_system_max, cpu_iowait_sum, cpu_iowait_max,
  cpu_steal_sum, cpu_steal_max, cpu_cores,
  mem_total, mem_used_sum, mem_used_max, mem_avail_sum, mem_usage_sum, mem_usage_max,
  swap_total, swap_used_sum, swap_used_max, swap_usage_sum, swap_usage_max,
  load1_sum, load1_max, load5_sum, load5_max, load15_sum, load15_max,
  uptime_max,
  disk_read_bytes, disk_read_ms, disk_read_bps_max,
  disk_write_bytes, disk_write_ms, disk_write_bps_max,
  net_rx_bytes, net_rx_ms, net_rx_bps_max,
  net_tx_bytes, net_tx_ms, net_tx_bps_max
) VALUES (
  ?,?,?,
  ?,?,?,?,
  ?,?,?,?,
  ?,?,?,
  ?,?,?,?,?,?,
  ?,?,?,?,?,
  ?,?,?,?,?,?,
  ?,
  ?,?,?,
  ?,?,?,
  ?,?,?,
  ?,?,?
) ON CONFLICT(res, ts) DO UPDATE SET
  sample_count = host_samples.sample_count + excluded.sample_count,
  cpu_usage_sum = host_samples.cpu_usage_sum + excluded.cpu_usage_sum,
  cpu_usage_max = MAX(host_samples.cpu_usage_max, excluded.cpu_usage_max),
  cpu_user_sum = host_samples.cpu_user_sum + excluded.cpu_user_sum,
  cpu_user_max = MAX(host_samples.cpu_user_max, excluded.cpu_user_max),
  cpu_system_sum = host_samples.cpu_system_sum + excluded.cpu_system_sum,
  cpu_system_max = MAX(host_samples.cpu_system_max, excluded.cpu_system_max),
  cpu_iowait_sum = host_samples.cpu_iowait_sum + excluded.cpu_iowait_sum,
  cpu_iowait_max = MAX(host_samples.cpu_iowait_max, excluded.cpu_iowait_max),
  cpu_steal_sum = host_samples.cpu_steal_sum + excluded.cpu_steal_sum,
  cpu_steal_max = MAX(host_samples.cpu_steal_max, excluded.cpu_steal_max),
  cpu_cores = MAX(host_samples.cpu_cores, excluded.cpu_cores),
  mem_total = MAX(host_samples.mem_total, excluded.mem_total),
  mem_used_sum = host_samples.mem_used_sum + excluded.mem_used_sum,
  mem_used_max = MAX(host_samples.mem_used_max, excluded.mem_used_max),
  mem_avail_sum = host_samples.mem_avail_sum + excluded.mem_avail_sum,
  mem_usage_sum = host_samples.mem_usage_sum + excluded.mem_usage_sum,
  mem_usage_max = MAX(host_samples.mem_usage_max, excluded.mem_usage_max),
  swap_total = MAX(host_samples.swap_total, excluded.swap_total),
  swap_used_sum = host_samples.swap_used_sum + excluded.swap_used_sum,
  swap_used_max = MAX(host_samples.swap_used_max, excluded.swap_used_max),
  swap_usage_sum = host_samples.swap_usage_sum + excluded.swap_usage_sum,
  swap_usage_max = MAX(host_samples.swap_usage_max, excluded.swap_usage_max),
  load1_sum = host_samples.load1_sum + excluded.load1_sum,
  load1_max = MAX(host_samples.load1_max, excluded.load1_max),
  load5_sum = host_samples.load5_sum + excluded.load5_sum,
  load5_max = MAX(host_samples.load5_max, excluded.load5_max),
  load15_sum = host_samples.load15_sum + excluded.load15_sum,
  load15_max = MAX(host_samples.load15_max, excluded.load15_max),
  uptime_max = MAX(host_samples.uptime_max, excluded.uptime_max),
  disk_read_bytes = host_samples.disk_read_bytes + excluded.disk_read_bytes,
  disk_read_ms = host_samples.disk_read_ms + excluded.disk_read_ms,
  disk_read_bps_max = MAX(host_samples.disk_read_bps_max, excluded.disk_read_bps_max),
  disk_write_bytes = host_samples.disk_write_bytes + excluded.disk_write_bytes,
  disk_write_ms = host_samples.disk_write_ms + excluded.disk_write_ms,
  disk_write_bps_max = MAX(host_samples.disk_write_bps_max, excluded.disk_write_bps_max),
  net_rx_bytes = host_samples.net_rx_bytes + excluded.net_rx_bytes,
  net_rx_ms = host_samples.net_rx_ms + excluded.net_rx_ms,
  net_rx_bps_max = MAX(host_samples.net_rx_bps_max, excluded.net_rx_bps_max),
  net_tx_bytes = host_samples.net_tx_bytes + excluded.net_tx_bytes,
  net_tx_ms = host_samples.net_tx_ms + excluded.net_tx_ms,
  net_tx_bps_max = MAX(host_samples.net_tx_bps_max, excluded.net_tx_bps_max)
`

// WriteHost stores one warm host sample at the raw resolution.
func (db *DB) WriteHost(ctx context.Context, s collect.HostSample) error {
	if !s.Warm {
		return nil
	}
	ms := s.Elapsed.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	ts := unixSeconds(s.Timestamp)
	err := db.exec(ctx, insertHost,
		ResRaw, ts, 1,
		s.CPUUsagePct, s.CPUUsagePct, s.CPUUserPct, s.CPUUserPct,
		s.CPUSystemPct, s.CPUSystemPct, s.CPUIOWaitPct, s.CPUIOWaitPct,
		s.CPUStealPct, s.CPUStealPct, s.CPUCores,
		s.MemTotal, s.MemUsed, s.MemUsed, s.MemAvailable, s.MemUsagePct, s.MemUsagePct,
		s.SwapTotal, s.SwapUsed, s.SwapUsed, s.SwapUsagePct, s.SwapUsagePct,
		s.Load1, s.Load1, s.Load5, s.Load5, s.Load15, s.Load15,
		int64(s.UptimeSeconds),
		s.DiskReadBytes, ms, s.DiskReadBps,
		s.DiskWriteBytes, ms, s.DiskWriteBps,
		s.NetRxBytes, ms, s.NetRxBps,
		s.NetTxBytes, ms, s.NetTxBps,
	)
	return err
}

const insertFS = `
INSERT INTO filesystem_samples (
  res, ts, mount, device, fstype, sample_count,
  total_bytes, used_sum, used_max, avail_sum, usage_sum, usage_max,
  inode_total, inode_used_sum, inode_used_max, inode_usage_sum, inode_usage_max
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(res, ts, mount) DO UPDATE SET
  device = excluded.device,
  fstype = excluded.fstype,
  sample_count = filesystem_samples.sample_count + excluded.sample_count,
  total_bytes = MAX(filesystem_samples.total_bytes, excluded.total_bytes),
  used_sum = filesystem_samples.used_sum + excluded.used_sum,
  used_max = MAX(filesystem_samples.used_max, excluded.used_max),
  avail_sum = filesystem_samples.avail_sum + excluded.avail_sum,
  usage_sum = filesystem_samples.usage_sum + excluded.usage_sum,
  usage_max = MAX(filesystem_samples.usage_max, excluded.usage_max),
  inode_total = MAX(filesystem_samples.inode_total, excluded.inode_total),
  inode_used_sum = filesystem_samples.inode_used_sum + excluded.inode_used_sum,
  inode_used_max = MAX(filesystem_samples.inode_used_max, excluded.inode_used_max),
  inode_usage_sum = filesystem_samples.inode_usage_sum + excluded.inode_usage_sum,
  inode_usage_max = MAX(filesystem_samples.inode_usage_max, excluded.inode_usage_max)
`

// WriteFilesystems stores capacity samples at the raw resolution.
func (db *DB) WriteFilesystems(ctx context.Context, at time.Time, samples []collect.FilesystemSample) error {
	if len(samples) == 0 {
		return nil
	}
	ts := unixSeconds(at)
	return db.withTx(ctx, func(tx *sql.Tx) error {
		for _, s := range samples {
			if s.Unavailable && s.Total == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, insertFS,
				ResRaw, ts, s.Mount, s.Device, s.FilesystemType, 1,
				s.Total, s.Used, s.Used, s.Available, s.UsagePct, s.UsagePct,
				s.InodeTotal, s.InodeUsed, s.InodeUsed, s.InodeUsagePct, s.InodeUsagePct,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

const upsertInstance = `
INSERT INTO process_instances (
  instance_id, boot_id, pid, starttime_ticks, ppid, started_at, name, executable,
  command_line, command_line_truncated, user_name, uid, kernel, first_seen, last_seen, exited
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)
ON CONFLICT(instance_id) DO UPDATE SET
  ppid = excluded.ppid,
  name = CASE WHEN excluded.name != '' THEN excluded.name ELSE process_instances.name END,
  executable = CASE WHEN excluded.executable != '' THEN excluded.executable ELSE process_instances.executable END,
  command_line = CASE WHEN excluded.command_line != '' THEN excluded.command_line ELSE process_instances.command_line END,
  command_line_truncated = excluded.command_line_truncated,
  user_name = CASE WHEN excluded.user_name != '' THEN excluded.user_name ELSE process_instances.user_name END,
  uid = excluded.uid,
  last_seen = MAX(process_instances.last_seen, excluded.last_seen),
  exited = 0
`

const insertProc = `
INSERT INTO process_samples (
  res, ts, instance_id, sample_count,
  cpu_sum, cpu_max, rss_sum, rss_max, mem_sum, mem_max,
  read_bytes, read_ms, read_bps_max,
  write_bytes, write_ms, write_bps_max,
  threads_sum, threads_max, state
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(res, ts, instance_id) DO UPDATE SET
  sample_count = process_samples.sample_count + excluded.sample_count,
  cpu_sum = process_samples.cpu_sum + excluded.cpu_sum,
  cpu_max = MAX(process_samples.cpu_max, excluded.cpu_max),
  rss_sum = process_samples.rss_sum + excluded.rss_sum,
  rss_max = MAX(process_samples.rss_max, excluded.rss_max),
  mem_sum = process_samples.mem_sum + excluded.mem_sum,
  mem_max = MAX(process_samples.mem_max, excluded.mem_max),
  read_bytes = process_samples.read_bytes + excluded.read_bytes,
  read_ms = process_samples.read_ms + excluded.read_ms,
  read_bps_max = MAX(process_samples.read_bps_max, excluded.read_bps_max),
  write_bytes = process_samples.write_bytes + excluded.write_bytes,
  write_ms = process_samples.write_ms + excluded.write_ms,
  write_bps_max = MAX(process_samples.write_bps_max, excluded.write_bps_max),
  threads_sum = process_samples.threads_sum + excluded.threads_sum,
  threads_max = MAX(process_samples.threads_max, excluded.threads_max),
  state = excluded.state
`

// WriteProcesses stores every process that has a rate, and marks exits.
// Command lines are stored as already truncated by the collector. They are
// not written to the daemon log.
func (db *DB) WriteProcesses(ctx context.Context, snap *collect.ProcessSnapshot) error {
	if snap == nil {
		return nil
	}
	ts := unixSeconds(snap.Timestamp)
	return db.withTx(ctx, func(tx *sql.Tx) error {
		for _, inst := range snap.First {
			if err := upsert(ctx, tx, inst, ts); err != nil {
				return err
			}
		}
		for _, p := range snap.Procs {
			ms := p.Elapsed.Milliseconds()
			if ms <= 0 {
				ms = 1
			}
			if err := upsert(ctx, tx, p.Instance, ts); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, insertProc,
				ResRaw, ts, p.ID(), 1,
				p.CPUPercent, p.CPUPercent, p.RSS, p.RSS, p.MemPercent, p.MemPercent,
				p.ReadBytes, ms, p.ReadBps,
				p.WriteBytes, ms, p.WriteBps,
				p.Threads, p.Threads, p.State,
			); err != nil {
				return err
			}
		}
		for _, id := range snap.Exited {
			if _, err := tx.ExecContext(ctx,
				`UPDATE process_instances SET exited = 1, last_seen = MAX(last_seen, ?) WHERE instance_id = ?`,
				ts, id.ID()); err != nil {
				return err
			}
		}
		return nil
	})
}

func upsert(ctx context.Context, tx *sql.Tx, inst collect.Instance, ts int64) error {
	kern, trunc := 0, 0
	if inst.Kernel {
		kern = 1
	}
	if inst.CmdTruncated {
		trunc = 1
	}
	_, err := tx.ExecContext(ctx, upsertInstance,
		inst.ID(), inst.BootID, inst.PID, inst.StartTicks, inst.PPID, inst.StartUnix, inst.Name, inst.Exe,
		inst.Cmd, trunc, inst.User, inst.UID, kern, ts, ts,
	)
	return err
}

func (db *DB) exec(ctx context.Context, query string, args ...any) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	})
}
