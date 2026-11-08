package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/api"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
)

// ProcessesAt returns the processes recorded nearest to at, including ones
// that have since exited. PID reuse does not merge them: rows are keyed by
// instance id.
func (db *DB) ProcessesAt(ctx context.Context, at time.Time, sortKey, name string, limit int) (api.ProcessList, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return api.ProcessList{}, moniterr.E("invalid_argument", "limit must be at most 100")
	}
	order, err := orderExpr(sortKey)
	if err != nil {
		return api.ProcessList{}, err
	}
	ts, res, err := db.nearestProcTS(ctx, at.Unix())
	if err != nil {
		return api.ProcessList{}, err
	}
	out := api.ProcessList{
		SchemaVersion: api.SchemaVersion,
		RequestedAt:   at.UTC().Format(time.RFC3339),
		Sort:          sortKey,
		Processes:     []api.Process{},
	}
	if sortKey == "" {
		out.Sort = "cpu"
	}
	if ts == 0 {
		return out, nil
	}
	out.At = formatUnix(ts)
	db.mu.Lock()
	defer db.mu.Unlock()
	q := `SELECT i.instance_id, i.pid, i.ppid, i.boot_id, i.starttime_ticks, i.started_at,
		i.name, i.executable, i.command_line, i.command_line_truncated, i.user_name, i.kernel,
		i.exited, i.last_seen, s.sample_count, s.cpu_sum, s.cpu_max, s.rss_max, s.mem_sum, s.mem_max,
		s.read_bytes, s.read_ms, s.write_bytes, s.write_ms, s.threads_max, s.state
		FROM process_samples s
		JOIN process_instances i ON i.instance_id = s.instance_id
		WHERE s.res = ? AND s.ts = ?`
	args := []any{res, ts}
	if name != "" {
		q += ` AND (instr(lower(i.name), lower(?)) > 0 OR instr(lower(i.command_line), lower(?)) > 0)`
		args = append(args, name, name)
	}
	q += ` ORDER BY ` + order + ` DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return api.ProcessList{}, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanProcess(rows, ts)
		if err != nil {
			return api.ProcessList{}, err
		}
		out.Processes = append(out.Processes, p)
	}
	return out, rows.Err()
}

// Process loads one instance and its latest sample.
func (db *DB) Process(ctx context.Context, id string) (api.Process, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	row := db.sql.QueryRowContext(ctx, `SELECT instance_id, pid, ppid, boot_id, starttime_ticks, started_at,
		name, executable, command_line, command_line_truncated, user_name, kernel, exited, last_seen
		FROM process_instances WHERE instance_id = ?`, id)
	var p api.Process
	var started, last int64
	var trunc, kernel, exited int
	var ticks int64
	if err := row.Scan(&p.InstanceID, &p.PID, &p.PPID, &p.BootID, &ticks, &started,
		&p.Name, &p.Executable, &p.CommandLine, &trunc, &p.User, &kernel, &exited, &last); err != nil {
		if err == sql.ErrNoRows {
			return api.Process{}, moniterr.E("not_found", "process instance not found")
		}
		return api.Process{}, err
	}
	p.StarttimeTicks = ticks
	p.StartedAt = formatUnix(started)
	p.LastSeen = formatUnix(last)
	p.CommandLineTruncated = trunc != 0
	p.Kernel = kernel != 0
	p.Exited = exited != 0
	sample := db.sql.QueryRowContext(ctx, `SELECT sample_count, cpu_sum, cpu_max, rss_max, mem_sum, mem_max,
		read_bytes, read_ms, write_bytes, write_ms, threads_max, state, ts
		FROM process_samples WHERE instance_id = ? ORDER BY ts DESC LIMIT 1`, id)
	var count, rss, readB, readMs, writeB, writeMs, threads, sampleTS int64
	var cpuSum, cpuMax, memSum, memMax float64
	var state string
	err := sample.Scan(&count, &cpuSum, &cpuMax, &rss, &memSum, &memMax, &readB, &readMs, &writeB, &writeMs, &threads, &state, &sampleTS)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return api.Process{}, err
	}
	p.SampleCount = int(count)
	p.CPUUsagePct = round2(avgF(cpuSum, count))
	p.CPUUsagePctMax = round2(cpuMax)
	p.RSSBytes = rss
	p.MemoryUsagePct = round2(avgF(memSum, count))
	p.ReadBytesPerSec = round2(rateOf(readB, readMs))
	p.WriteBytesPerSec = round2(rateOf(writeB, writeMs))
	p.ThreadCount = int(threads)
	p.State = state
	if sampleTS > 0 && p.LastSeen == "" {
		p.LastSeen = formatUnix(sampleTS)
	}
	return p, nil
}

func (db *DB) nearestProcTS(ctx context.Context, ts int64) (int64, int64, error) {
	tiers := db.Tiers()
	type cand struct{ res, tol int64 }
	cands := []cand{{ResRaw, tiers.RawInterval * 2}}
	for _, w := range []int64{tiers.Short, tiers.Medium, tiers.Long, tiers.Archive} {
		if w > 0 {
			cands = append(cands, cand{w, w})
		}
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	var bestTS, bestRes, bestDelta int64
	found := false
	for _, c := range cands {
		tol := c.tol
		if tol < 1 {
			tol = 1
		}
		var got sql.NullInt64
		err := db.sql.QueryRowContext(ctx, `SELECT ts FROM process_samples
			WHERE res = ? AND ts >= ? AND ts <= ?
			ORDER BY ABS(ts - ?) LIMIT 1`, c.res, ts-tol, ts+tol, ts).Scan(&got)
		if err == sql.ErrNoRows || !got.Valid {
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		delta := got.Int64 - ts
		if delta < 0 {
			delta = -delta
		}
		if !found || delta < bestDelta || (delta == bestDelta && (c.res == ResRaw || (bestRes != ResRaw && c.res < bestRes))) {
			found = true
			bestTS, bestRes, bestDelta = got.Int64, c.res, delta
		}
	}
	return bestTS, bestRes, nil
}

func orderExpr(sortKey string) (string, error) {
	switch sortKey {
	case "", "cpu":
		return "s.cpu_max", nil
	case "memory":
		return "s.mem_max", nil
	case "rss":
		return "s.rss_max", nil
	case "read":
		return "s.read_bps_max", nil
	case "write":
		return "s.write_bps_max", nil
	default:
		return "", moniterr.E("invalid_argument", "sort must be cpu, memory, rss, read, or write")
	}
}

type rower interface {
	Scan(dest ...any) error
}

func scanProcess(rows rower, sampleTS int64) (api.Process, error) {
	var p api.Process
	var started, last, ticks, count, rss, readB, readMs, writeB, writeMs, threads int64
	var trunc, kernel, exited int
	var cpuSum, cpuMax, memSum, memMax float64
	if err := rows.Scan(&p.InstanceID, &p.PID, &p.PPID, &p.BootID, &ticks, &started,
		&p.Name, &p.Executable, &p.CommandLine, &trunc, &p.User, &kernel,
		&exited, &last, &count, &cpuSum, &cpuMax, &rss, &memSum, &memMax,
		&readB, &readMs, &writeB, &writeMs, &threads, &p.State); err != nil {
		return api.Process{}, err
	}
	p.StarttimeTicks = ticks
	p.StartedAt = formatUnix(started)
	p.LastSeen = formatUnix(last)
	p.CommandLineTruncated = trunc != 0
	p.Kernel = kernel != 0
	p.Exited = exited != 0 || last <= sampleTS
	p.SampleCount = int(count)
	p.CPUUsagePct = round2(avgF(cpuSum, count))
	p.CPUUsagePctMax = round2(cpuMax)
	p.RSSBytes = rss
	p.MemoryUsagePct = round2(maxF(avgF(memSum, count), memMax))
	if count > 0 {
		p.MemoryUsagePct = round2(avgF(memSum, count))
	}
	p.ReadBytesPerSec = round2(rateOf(readB, readMs))
	p.WriteBytesPerSec = round2(rateOf(writeB, writeMs))
	p.ThreadCount = int(threads)
	return p, nil
}

// TierInfos describes the configured tiers for storage status.
func (db *DB) TierInfos() []api.TierInfo {
	t := db.Tiers()
	return []api.TierInfo{
		{Name: "raw", ResolutionSeconds: t.RawInterval, RetainSeconds: t.RawRetain},
		{Name: "short", ResolutionSeconds: t.Short, RetainSeconds: t.ShortRetain},
		{Name: "medium", ResolutionSeconds: t.Medium, RetainSeconds: t.MediumRetain},
		{Name: "long", ResolutionSeconds: t.Long, RetainSeconds: t.LongRetain},
		{Name: "archive", ResolutionSeconds: t.Archive, RetainSeconds: t.MaxRetain},
	}
}

// DebugResCount is used by tests to assert which resolution holds a timestamp.
func (db *DB) DebugResCount(ctx context.Context, table string, res, ts int64) (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if table != "host_samples" && table != "process_samples" && table != "filesystem_samples" {
		return 0, fmt.Errorf("unknown table")
	}
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE res = ? AND ts = ?`, res, ts).Scan(&n)
	return n, err
}
