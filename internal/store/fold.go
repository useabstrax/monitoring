package store

import (
	"context"
	"database/sql"
	"sort"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/api"
)

type hostRow struct {
	res, ts, count                                               int64
	cpuUsageSum, cpuUsageMax                                     float64
	cpuUserSum, cpuUserMax                                       float64
	cpuSystemSum, cpuSystemMax                                   float64
	cpuIOWaitSum, cpuIOWaitMax                                   float64
	cpuStealSum, cpuStealMax                                     float64
	cores                                                        int64
	memTotal, memUsedSum, memUsedMax, memAvailSum                int64
	memUsageSum, memUsageMax                                     float64
	swapTotal, swapUsedSum, swapUsedMax                          int64
	swapUsageSum, swapUsageMax                                   float64
	load1Sum, load1Max, load5Sum, load5Max, load15Sum, load15Max float64
	uptime                                                       int64
	diskReadBytes, diskReadMs                                    int64
	diskReadMax                                                  float64
	diskWriteBytes, diskWriteMs                                  int64
	diskWriteMax                                                 float64
	netRxBytes, netRxMs                                          int64
	netRxMax                                                     float64
	netTxBytes, netTxMs                                          int64
	netTxMax                                                     float64
}

func (a *hostRow) add(b hostRow) {
	if a.count == 0 {
		*a = b
		return
	}
	a.count += b.count
	a.cpuUsageSum += b.cpuUsageSum
	a.cpuUsageMax = maxF(a.cpuUsageMax, b.cpuUsageMax)
	a.cpuUserSum += b.cpuUserSum
	a.cpuUserMax = maxF(a.cpuUserMax, b.cpuUserMax)
	a.cpuSystemSum += b.cpuSystemSum
	a.cpuSystemMax = maxF(a.cpuSystemMax, b.cpuSystemMax)
	a.cpuIOWaitSum += b.cpuIOWaitSum
	a.cpuIOWaitMax = maxF(a.cpuIOWaitMax, b.cpuIOWaitMax)
	a.cpuStealSum += b.cpuStealSum
	a.cpuStealMax = maxF(a.cpuStealMax, b.cpuStealMax)
	if b.cores > a.cores {
		a.cores = b.cores
	}
	if b.memTotal > a.memTotal {
		a.memTotal = b.memTotal
	}
	a.memUsedSum += b.memUsedSum
	if b.memUsedMax > a.memUsedMax {
		a.memUsedMax = b.memUsedMax
	}
	a.memAvailSum += b.memAvailSum
	a.memUsageSum += b.memUsageSum
	a.memUsageMax = maxF(a.memUsageMax, b.memUsageMax)
	if b.swapTotal > a.swapTotal {
		a.swapTotal = b.swapTotal
	}
	a.swapUsedSum += b.swapUsedSum
	if b.swapUsedMax > a.swapUsedMax {
		a.swapUsedMax = b.swapUsedMax
	}
	a.swapUsageSum += b.swapUsageSum
	a.swapUsageMax = maxF(a.swapUsageMax, b.swapUsageMax)
	a.load1Sum += b.load1Sum
	a.load1Max = maxF(a.load1Max, b.load1Max)
	a.load5Sum += b.load5Sum
	a.load5Max = maxF(a.load5Max, b.load5Max)
	a.load15Sum += b.load15Sum
	a.load15Max = maxF(a.load15Max, b.load15Max)
	if b.uptime > a.uptime {
		a.uptime = b.uptime
	}
	a.diskReadBytes += b.diskReadBytes
	a.diskReadMs += b.diskReadMs
	a.diskReadMax = maxF(a.diskReadMax, b.diskReadMax)
	a.diskWriteBytes += b.diskWriteBytes
	a.diskWriteMs += b.diskWriteMs
	a.diskWriteMax = maxF(a.diskWriteMax, b.diskWriteMax)
	a.netRxBytes += b.netRxBytes
	a.netRxMs += b.netRxMs
	a.netRxMax = maxF(a.netRxMax, b.netRxMax)
	a.netTxBytes += b.netTxBytes
	a.netTxMs += b.netTxMs
	a.netTxMax = maxF(a.netTxMax, b.netTxMax)
}

func maxF(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

const hostSelect = `SELECT res, ts, sample_count,
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
FROM host_samples WHERE ts >= ? AND ts < ? ORDER BY ts`

func (db *DB) loadHost(ctx context.Context, from, to int64) ([]hostRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	rows, err := db.sql.QueryContext(ctx, hostSelect, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hostRow
	for rows.Next() {
		var r hostRow
		if err := rows.Scan(
			&r.res, &r.ts, &r.count,
			&r.cpuUsageSum, &r.cpuUsageMax, &r.cpuUserSum, &r.cpuUserMax,
			&r.cpuSystemSum, &r.cpuSystemMax, &r.cpuIOWaitSum, &r.cpuIOWaitMax,
			&r.cpuStealSum, &r.cpuStealMax, &r.cores,
			&r.memTotal, &r.memUsedSum, &r.memUsedMax, &r.memAvailSum, &r.memUsageSum, &r.memUsageMax,
			&r.swapTotal, &r.swapUsedSum, &r.swapUsedMax, &r.swapUsageSum, &r.swapUsageMax,
			&r.load1Sum, &r.load1Max, &r.load5Sum, &r.load5Max, &r.load15Sum, &r.load15Max,
			&r.uptime,
			&r.diskReadBytes, &r.diskReadMs, &r.diskReadMax,
			&r.diskWriteBytes, &r.diskWriteMs, &r.diskWriteMax,
			&r.netRxBytes, &r.netRxMs, &r.netRxMax,
			&r.netTxBytes, &r.netTxMs, &r.netTxMax,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func foldHost(rows []hostRow, width int64) []api.HostPoint {
	if len(rows) == 0 {
		return []api.HostPoint{}
	}
	acc := map[int64]*hostRow{}
	var keys []int64
	for _, r := range rows {
		b := alignDown(r.ts, width)
		if acc[b] == nil {
			acc[b] = &hostRow{}
			keys = append(keys, b)
		}
		acc[b].add(r)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]api.HostPoint, 0, len(keys))
	for _, k := range keys {
		r := acc[k]
		n := r.count
		out = append(out, api.HostPoint{
			Timestamp:                     formatUnix(k),
			SampleCount:                   int(n),
			CPUUsagePctAvg:                round2(avgF(r.cpuUsageSum, n)),
			CPUUsagePctMax:                round2(r.cpuUsageMax),
			CPUUserPctAvg:                 round2(avgF(r.cpuUserSum, n)),
			CPUUserPctMax:                 round2(r.cpuUserMax),
			CPUSystemPctAvg:               round2(avgF(r.cpuSystemSum, n)),
			CPUSystemPctMax:               round2(r.cpuSystemMax),
			CPUIOWaitPctAvg:               round2(avgF(r.cpuIOWaitSum, n)),
			CPUIOWaitPctMax:               round2(r.cpuIOWaitMax),
			CPUStealPctAvg:                round2(avgF(r.cpuStealSum, n)),
			CPUStealPctMax:                round2(r.cpuStealMax),
			CPUCores:                      int(r.cores),
			MemoryTotalBytes:              r.memTotal,
			MemoryUsedBytesAvg:            divRound(r.memUsedSum, n),
			MemoryUsedBytesMax:            r.memUsedMax,
			MemoryAvailableBytesAvg:       divRound(r.memAvailSum, n),
			MemoryUsagePctAvg:             round2(avgF(r.memUsageSum, n)),
			MemoryUsagePctMax:             round2(r.memUsageMax),
			SwapTotalBytes:                r.swapTotal,
			SwapUsedBytesAvg:              divRound(r.swapUsedSum, n),
			SwapUsedBytesMax:              r.swapUsedMax,
			SwapUsagePctAvg:               round2(avgF(r.swapUsageSum, n)),
			SwapUsagePctMax:               round2(r.swapUsageMax),
			Load1mAvg:                     round2(avgF(r.load1Sum, n)),
			Load1mMax:                     round2(r.load1Max),
			Load5mAvg:                     round2(avgF(r.load5Sum, n)),
			Load5mMax:                     round2(r.load5Max),
			Load15mAvg:                    round2(avgF(r.load15Sum, n)),
			Load15mMax:                    round2(r.load15Max),
			UptimeSeconds:                 r.uptime,
			DiskReadBytesPerSecAvg:        round2(rateOf(r.diskReadBytes, r.diskReadMs)),
			DiskReadBytesPerSecMax:        round2(r.diskReadMax),
			DiskWriteBytesPerSecAvg:       round2(rateOf(r.diskWriteBytes, r.diskWriteMs)),
			DiskWriteBytesPerSecMax:       round2(r.diskWriteMax),
			NetworkReceiveBytesPerSecAvg:  round2(rateOf(r.netRxBytes, r.netRxMs)),
			NetworkReceiveBytesPerSecMax:  round2(r.netRxMax),
			NetworkTransmitBytesPerSecAvg: round2(rateOf(r.netTxBytes, r.netTxMs)),
			NetworkTransmitBytesPerSecMax: round2(r.netTxMax),
		})
	}
	return out
}

type fsRow struct {
	ts, count                              int64
	mount, device, fstype                  string
	total, usedSum, usedMax, availSum      int64
	usageSum, usageMax                     float64
	inodeTotal, inodeUsedSum, inodeUsedMax int64
	inodeUsageSum, inodeUsageMax           float64
}

func (a *fsRow) add(b fsRow) {
	if a.count == 0 {
		*a = b
		return
	}
	a.count += b.count
	if b.device != "" {
		a.device = b.device
	}
	if b.fstype != "" {
		a.fstype = b.fstype
	}
	if b.total > a.total {
		a.total = b.total
	}
	a.usedSum += b.usedSum
	if b.usedMax > a.usedMax {
		a.usedMax = b.usedMax
	}
	a.availSum += b.availSum
	a.usageSum += b.usageSum
	a.usageMax = maxF(a.usageMax, b.usageMax)
	if b.inodeTotal > a.inodeTotal {
		a.inodeTotal = b.inodeTotal
	}
	a.inodeUsedSum += b.inodeUsedSum
	if b.inodeUsedMax > a.inodeUsedMax {
		a.inodeUsedMax = b.inodeUsedMax
	}
	a.inodeUsageSum += b.inodeUsageSum
	a.inodeUsageMax = maxF(a.inodeUsageMax, b.inodeUsageMax)
}

func (db *DB) loadFS(ctx context.Context, from, to int64) ([]fsRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	rows, err := db.sql.QueryContext(ctx, `SELECT ts, mount, device, fstype, sample_count,
		total_bytes, used_sum, used_max, avail_sum, usage_sum, usage_max,
		inode_total, inode_used_sum, inode_used_max, inode_usage_sum, inode_usage_max
		FROM filesystem_samples WHERE ts >= ? AND ts < ? ORDER BY mount, ts`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fsRow
	for rows.Next() {
		var r fsRow
		if err := rows.Scan(&r.ts, &r.mount, &r.device, &r.fstype, &r.count,
			&r.total, &r.usedSum, &r.usedMax, &r.availSum, &r.usageSum, &r.usageMax,
			&r.inodeTotal, &r.inodeUsedSum, &r.inodeUsedMax, &r.inodeUsageSum, &r.inodeUsageMax); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func foldFS(rows []fsRow, width int64) []api.FilesystemSeries {
	type key struct {
		mount  string
		bucket int64
	}
	acc := map[key]*fsRow{}
	var mounts []string
	seenMount := map[string]bool{}
	for _, r := range rows {
		k := key{r.mount, alignDown(r.ts, width)}
		if acc[k] == nil {
			acc[k] = &fsRow{mount: r.mount}
		}
		acc[k].add(r)
		if !seenMount[r.mount] {
			seenMount[r.mount] = true
			mounts = append(mounts, r.mount)
		}
	}
	sort.Strings(mounts)
	out := make([]api.FilesystemSeries, 0, len(mounts))
	for _, m := range mounts {
		var buckets []int64
		for k := range acc {
			if k.mount == m {
				buckets = append(buckets, k.bucket)
			}
		}
		sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
		series := api.FilesystemSeries{Mount: m, Points: []api.FilesystemPoint{}}
		for _, b := range buckets {
			r := acc[key{m, b}]
			if series.Device == "" {
				series.Device = r.device
				series.FilesystemType = r.fstype
			}
			n := r.count
			series.Points = append(series.Points, api.FilesystemPoint{
				Timestamp:         formatUnix(b),
				SampleCount:       int(n),
				TotalBytes:        r.total,
				UsedBytesAvg:      divRound(r.usedSum, n),
				UsedBytesMax:      r.usedMax,
				AvailableBytesAvg: divRound(r.availSum, n),
				UsagePctAvg:       round2(avgF(r.usageSum, n)),
				UsagePctMax:       round2(r.usageMax),
				InodeTotal:        r.inodeTotal,
				InodeUsedAvg:      divRound(r.inodeUsedSum, n),
				InodeUsedMax:      r.inodeUsedMax,
				InodeUsagePctAvg:  round2(avgF(r.inodeUsageSum, n)),
				InodeUsagePctMax:  round2(r.inodeUsageMax),
			})
		}
		out = append(out, series)
	}
	return out
}

type procRow struct {
	ts, count              int64
	cpuSum, cpuMax         float64
	rssSum, rssMax         int64
	memSum, memMax         float64
	readBytes, readMs      int64
	readMax                float64
	writeBytes, writeMs    int64
	writeMax               float64
	threadsSum, threadsMax int64
	state                  string
}

func (a *procRow) add(b procRow) {
	if a.count == 0 {
		*a = b
		return
	}
	a.count += b.count
	a.cpuSum += b.cpuSum
	a.cpuMax = maxF(a.cpuMax, b.cpuMax)
	a.rssSum += b.rssSum
	if b.rssMax > a.rssMax {
		a.rssMax = b.rssMax
	}
	a.memSum += b.memSum
	a.memMax = maxF(a.memMax, b.memMax)
	a.readBytes += b.readBytes
	a.readMs += b.readMs
	a.readMax = maxF(a.readMax, b.readMax)
	a.writeBytes += b.writeBytes
	a.writeMs += b.writeMs
	a.writeMax = maxF(a.writeMax, b.writeMax)
	a.threadsSum += b.threadsSum
	if b.threadsMax > a.threadsMax {
		a.threadsMax = b.threadsMax
	}
	if b.state != "" {
		a.state = b.state
	}
}

func (db *DB) loadProc(ctx context.Context, id string, from, to int64) ([]procRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	rows, err := db.sql.QueryContext(ctx, `SELECT ts, sample_count, cpu_sum, cpu_max, rss_sum, rss_max,
		mem_sum, mem_max, read_bytes, read_ms, read_bps_max, write_bytes, write_ms, write_bps_max,
		threads_sum, threads_max, state
		FROM process_samples WHERE instance_id = ? AND ts >= ? AND ts < ? ORDER BY ts`, id, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []procRow
	for rows.Next() {
		var r procRow
		if err := rows.Scan(&r.ts, &r.count, &r.cpuSum, &r.cpuMax, &r.rssSum, &r.rssMax,
			&r.memSum, &r.memMax, &r.readBytes, &r.readMs, &r.readMax, &r.writeBytes, &r.writeMs, &r.writeMax,
			&r.threadsSum, &r.threadsMax, &r.state); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func foldProc(rows []procRow, width int64) []api.ProcessPoint {
	acc := map[int64]*procRow{}
	var keys []int64
	for _, r := range rows {
		b := alignDown(r.ts, width)
		if acc[b] == nil {
			acc[b] = &procRow{}
			keys = append(keys, b)
		}
		acc[b].add(r)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]api.ProcessPoint, 0, len(keys))
	for _, k := range keys {
		r := acc[k]
		n := r.count
		out = append(out, api.ProcessPoint{
			Timestamp:           formatUnix(k),
			SampleCount:         int(n),
			CPUUsagePctAvg:      round2(avgF(r.cpuSum, n)),
			CPUUsagePctMax:      round2(r.cpuMax),
			RSSBytesAvg:         divRound(r.rssSum, n),
			RSSBytesMax:         r.rssMax,
			MemoryUsagePctAvg:   round2(avgF(r.memSum, n)),
			MemoryUsagePctMax:   round2(r.memMax),
			ReadBytesPerSecAvg:  round2(rateOf(r.readBytes, r.readMs)),
			ReadBytesPerSecMax:  round2(r.readMax),
			WriteBytesPerSecAvg: round2(rateOf(r.writeBytes, r.writeMs)),
			WriteBytesPerSecMax: round2(r.writeMax),
			ThreadCountAvg:      int(divRound(r.threadsSum, n)),
			ThreadCountMax:      int(r.threadsMax),
			State:               r.state,
		})
	}
	return out
}

// Span is the retained time range and instance count.
type Span struct {
	Oldest, Newest int64
	Instances      int64
}

// Span reads the oldest and newest host sample and the process instance count.
func (db *DB) Span(ctx context.Context) (Span, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var s Span
	var min, max sql.NullInt64
	if err := db.sql.QueryRowContext(ctx, `SELECT MIN(ts), MAX(ts) FROM host_samples`).Scan(&min, &max); err != nil {
		return Span{}, err
	}
	s.Oldest, s.Newest = min.Int64, max.Int64
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM process_instances`).Scan(&s.Instances); err != nil {
		return Span{}, err
	}
	return s, nil
}
