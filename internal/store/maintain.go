package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Maintain rolls aged samples into coarser tiers, drops data past the
// retention horizon, and then enforces the byte ceiling. Rollup and deletion
// of a tier happen in one transaction.
func (db *DB) Maintain(ctx context.Context, now time.Time) error {
	tiers := db.Tiers()
	nowUnix := now.Unix()
	steps := []struct{ from, to, retain int64 }{
		{ResRaw, tiers.Short, tiers.RawRetain},
		{tiers.Short, tiers.Medium, tiers.ShortRetain},
		{tiers.Medium, tiers.Long, tiers.MediumRetain},
		{tiers.Long, tiers.Archive, tiers.LongRetain},
	}
	for _, step := range steps {
		if step.to <= 0 || step.retain <= 0 || step.from >= step.to && step.from != ResRaw {
			continue
		}
		if step.from != ResRaw && step.from >= step.to {
			continue
		}
		cutoff := alignDown(nowUnix-step.retain, step.to)
		if cutoff <= 0 {
			continue
		}
		if err := db.rollup(ctx, step.from, step.to, cutoff); err != nil {
			return err
		}
	}
	if tiers.MaxRetain > 0 {
		if err := db.purgeBefore(ctx, nowUnix-tiers.MaxRetain); err != nil {
			return err
		}
	}
	if err := db.enforceCeiling(ctx, nowUnix, tiers); err != nil {
		return err
	}
	db.checkpoint(ctx)
	return nil
}

func (db *DB) rollup(ctx context.Context, fromRes, toRes, cutoff int64) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, rollupHostSQL, toRes, toRes, toRes, fromRes, cutoff, toRes); err != nil {
			return fmt.Errorf("rollup host %d->%d: %w", fromRes, toRes, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM host_samples WHERE res = ? AND ts < ?`, fromRes, cutoff); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, rollupFSSQL, toRes, toRes, toRes, fromRes, cutoff, toRes); err != nil {
			return fmt.Errorf("rollup filesystem %d->%d: %w", fromRes, toRes, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM filesystem_samples WHERE res = ? AND ts < ?`, fromRes, cutoff); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, rollupProcSQL, toRes, toRes, toRes, fromRes, cutoff, toRes); err != nil {
			return fmt.Errorf("rollup process %d->%d: %w", fromRes, toRes, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM process_samples WHERE res = ? AND ts < ?`, fromRes, cutoff); err != nil {
			return err
		}
		return nil
	})
}

func (db *DB) purgeBefore(ctx context.Context, before int64) error {
	if before <= 0 {
		return nil
	}
	return db.deleteBefore(ctx, before)
}

func (db *DB) deleteBefore(ctx context.Context, before int64) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM host_samples WHERE ts < ?`,
			`DELETE FROM filesystem_samples WHERE ts < ?`,
			`DELETE FROM process_samples WHERE ts < ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, before); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
			DELETE FROM process_instances
			 WHERE last_seen < ?
			   AND NOT EXISTS (SELECT 1 FROM process_samples s WHERE s.instance_id = process_instances.instance_id)`, before)
		return err
	})
}

func (db *DB) enforceCeiling(ctx context.Context, now int64, tiers Tiers) error {
	if tiers.MaxBytes <= 0 {
		return nil
	}
	keep := now - tiers.RawInterval*2
	if keep < 0 {
		keep = 0
	}
	step := tiers.Archive
	if step < 60 {
		step = 60
	}
	for i := 0; i < 200 && db.SizeBytes() > tiers.MaxBytes; i++ {
		oldest, err := db.oldestTS(ctx)
		if err != nil || oldest == 0 || oldest >= keep {
			break
		}
		cutoff := oldest + step
		if cutoff > keep {
			cutoff = keep
		}
		if cutoff <= oldest {
			break
		}
		if err := db.deleteBefore(ctx, cutoff); err != nil {
			return err
		}
		db.vacuum(ctx, 500)
		db.checkpoint(ctx)
	}
	return nil
}

func (db *DB) oldestTS(ctx context.Context) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var oldest sql.NullInt64
	err := db.sql.QueryRowContext(ctx, `
		SELECT MIN(ts) FROM (
			SELECT MIN(ts) AS ts FROM host_samples
			UNION ALL SELECT MIN(ts) FROM filesystem_samples
			UNION ALL SELECT MIN(ts) FROM process_samples
		)`).Scan(&oldest)
	if err != nil {
		return 0, err
	}
	if !oldest.Valid {
		return 0, nil
	}
	return oldest.Int64, nil
}

func (db *DB) checkpoint(ctx context.Context) {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, _ = db.sql.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
}

func (db *DB) vacuum(ctx context.Context, pages int) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var free int64
	_ = db.sql.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free)
	if free == 0 {
		return
	}
	_, _ = db.sql.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, pages))
}

func alignDown(ts, width int64) int64 {
	if width <= 1 || ts <= 0 {
		return ts
	}
	return ts - ts%width
}

// StorageConstrained reports whether the byte ceiling, rather than the
// configured horizon, is what limits history. A young database that has not
// had time to fill is not treated as constrained.
func StorageConstrained(now, oldest, maxRetain, archive, size, maxBytes int64) bool {
	if oldest <= 0 || maxRetain <= 0 || maxBytes <= 0 {
		return false
	}
	effective := now - oldest
	if effective+archive >= maxRetain {
		return false
	}
	return size*10 >= maxBytes*8
}

const rollupHostSQL = `
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
)
SELECT ?, (ts / ?) * ?,
  SUM(sample_count),
  SUM(cpu_usage_sum), MAX(cpu_usage_max), SUM(cpu_user_sum), MAX(cpu_user_max),
  SUM(cpu_system_sum), MAX(cpu_system_max), SUM(cpu_iowait_sum), MAX(cpu_iowait_max),
  SUM(cpu_steal_sum), MAX(cpu_steal_max), MAX(cpu_cores),
  MAX(mem_total), SUM(mem_used_sum), MAX(mem_used_max), SUM(mem_avail_sum), SUM(mem_usage_sum), MAX(mem_usage_max),
  MAX(swap_total), SUM(swap_used_sum), MAX(swap_used_max), SUM(swap_usage_sum), MAX(swap_usage_max),
  SUM(load1_sum), MAX(load1_max), SUM(load5_sum), MAX(load5_max), SUM(load15_sum), MAX(load15_max),
  MAX(uptime_max),
  SUM(disk_read_bytes), SUM(disk_read_ms), MAX(disk_read_bps_max),
  SUM(disk_write_bytes), SUM(disk_write_ms), MAX(disk_write_bps_max),
  SUM(net_rx_bytes), SUM(net_rx_ms), MAX(net_rx_bps_max),
  SUM(net_tx_bytes), SUM(net_tx_ms), MAX(net_tx_bps_max)
FROM host_samples
WHERE res = ? AND ts < ?
GROUP BY ts / ?
ON CONFLICT(res, ts) DO NOTHING`

const rollupFSSQL = `
INSERT INTO filesystem_samples (
  res, ts, mount, device, fstype, sample_count,
  total_bytes, used_sum, used_max, avail_sum, usage_sum, usage_max,
  inode_total, inode_used_sum, inode_used_max, inode_usage_sum, inode_usage_max
)
SELECT ?, (ts / ?) * ?, mount,
  MAX(device), MAX(fstype), SUM(sample_count),
  MAX(total_bytes), SUM(used_sum), MAX(used_max), SUM(avail_sum), SUM(usage_sum), MAX(usage_max),
  MAX(inode_total), SUM(inode_used_sum), MAX(inode_used_max), SUM(inode_usage_sum), MAX(inode_usage_max)
FROM filesystem_samples
WHERE res = ? AND ts < ?
GROUP BY mount, ts / ?
ON CONFLICT(res, ts, mount) DO NOTHING`

const rollupProcSQL = `
INSERT INTO process_samples (
  res, ts, instance_id, sample_count,
  cpu_sum, cpu_max, rss_sum, rss_max, mem_sum, mem_max,
  read_bytes, read_ms, read_bps_max,
  write_bytes, write_ms, write_bps_max,
  threads_sum, threads_max, state
)
SELECT ?, (ts / ?) * ?, instance_id,
  SUM(sample_count),
  SUM(cpu_sum), MAX(cpu_max), SUM(rss_sum), MAX(rss_max), SUM(mem_sum), MAX(mem_max),
  SUM(read_bytes), SUM(read_ms), MAX(read_bps_max),
  SUM(write_bytes), SUM(write_ms), MAX(write_bps_max),
  SUM(threads_sum), MAX(threads_max), MAX(state)
FROM process_samples
WHERE res = ? AND ts < ?
GROUP BY instance_id, ts / ?
ON CONFLICT(res, ts, instance_id) DO NOTHING`
