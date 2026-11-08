// Package daemon runs collection, storage, retention, and the local socket.
package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/api"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/collect"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/config"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/ipc"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/layout"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/plugin"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/store"
)

// Daemon owns collection and the monitoring database.
type Daemon struct {
	paths layout.Paths
	log   *slog.Logger
	store *store.DB
	host  *collect.Host
	proc  *collect.Process

	cfg   atomicConfig
	bcast *broadcast

	started time.Time

	mu          sync.Mutex
	latest      api.Latest
	filesystems []api.FilesystemLatest
	processes   []api.Process
	live        []byte

	subMu sync.Mutex
	subs  []*subscriber
}

type subscriber struct {
	ch     chan []byte
	closed bool
}

type atomicConfig struct {
	mu sync.Mutex
	c  *config.Config
}

func (a *atomicConfig) Store(c *config.Config) {
	a.mu.Lock()
	a.c = c
	a.mu.Unlock()
}

func (a *atomicConfig) Load() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.c
}

type broadcast struct {
	mu sync.Mutex
	ch chan struct{}
}

func newBroadcast() *broadcast { return &broadcast{ch: make(chan struct{})} }

func (b *broadcast) Notify() {
	b.mu.Lock()
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

func (b *broadcast) C() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

// New builds a daemon. db may be nil; Run opens the database.
func New(cfg *config.Config, paths layout.Paths, host *collect.Host, proc *collect.Process, db *store.DB, log *slog.Logger) *Daemon {
	if log == nil {
		log = slog.Default()
	}
	if host == nil {
		host = collect.NewHost("/proc", "/sys")
	}
	if proc == nil {
		proc = collect.NewProcess("/proc", 0)
	}
	d := &Daemon{
		paths: paths, log: log, store: db, host: host, proc: proc,
		bcast: newBroadcast(), started: time.Now(),
		latest: api.Latest{SchemaVersion: api.SchemaVersion, Filesystems: []api.FilesystemLatest{}, TopProcesses: []api.Process{}},
	}
	d.apply(cfg)
	return d
}

func (d *Daemon) apply(cfg *config.Config) {
	d.cfg.Store(cfg)
	d.host.SetStatfsTimeout(cfg.Filesystems.Timeout.D())
	d.host.SetExclude(cfg.Filesystems.Exclude)
	d.proc.SetMaxCommandLine(cfg.Process.CommandLineMaxBytes)
	if d.store != nil {
		d.store.SetTiers(Tiers(cfg))
	}
}

// Tiers converts configuration into the store retention schedule.
func Tiers(cfg *config.Config) store.Tiers {
	return store.Tiers{
		RawInterval:  cfg.Sampling.HostInterval.Seconds(),
		RawRetain:    cfg.Retention.Raw.Seconds(),
		Short:        cfg.Rollups.ShortInterval.Seconds(),
		ShortRetain:  cfg.Retention.Short.Seconds(),
		Medium:       cfg.Rollups.MediumInterval.Seconds(),
		MediumRetain: cfg.Retention.Medium.Seconds(),
		Long:         cfg.Rollups.LongInterval.Seconds(),
		LongRetain:   cfg.Retention.Long.Seconds(),
		Archive:      cfg.Rollups.ArchiveInterval.Seconds(),
		MaxRetain:    cfg.Retention.Maximum.Seconds(),
		MaxBytes:     cfg.Storage.MaxSize.Bytes(),
	}
}

// Run collects until ctx is cancelled, then closes the database.
func (d *Daemon) Run(ctx context.Context) error {
	if d.store == nil {
		db, err := store.Open(d.paths.Database(), Tiers(d.cfg.Load()))
		if err != nil {
			return err
		}
		d.store = db
	}
	if err := layout.EnsureDir(d.paths.StateDir); err != nil {
		return err
	}
	ln, err := ipc.Listen(d.paths.Socket())
	if err != nil {
		return err
	}
	go ipc.Serve(ctx, ln, d)

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); d.hostLoop(ctx) }()
	go func() { defer wg.Done(); d.fsLoop(ctx) }()
	go func() { defer wg.Done(); d.procLoop(ctx) }()
	go func() { defer wg.Done(); d.maintainLoop(ctx) }()

	<-ctx.Done()
	_ = ln.Close()
	wg.Wait()
	return d.store.Close()
}

func (d *Daemon) hostLoop(ctx context.Context) {
	if s, err := d.host.Collect(time.Now()); err == nil {
		d.noteHost(s)
	}
	for ctx.Err() == nil {
		cfg := d.cfg.Load()
		if !wait(ctx, d.bcast, cfg.Sampling.HostInterval.D()) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		s, err := d.host.Collect(time.Now())
		if err != nil {
			d.log.Warn("host collection failed", "err", err)
			continue
		}
		d.proc.SetMemTotal(s.MemTotal)
		d.noteHost(s)
		if !s.Warm {
			continue
		}
		if err := d.store.WriteHost(ctx, *s); err != nil {
			d.log.Warn("writing host sample", "err", err)
			continue
		}
		d.writeSnapshot(s)
		d.publish()
	}
}

func (d *Daemon) fsLoop(ctx context.Context) {
	for ctx.Err() == nil {
		now := time.Now()
		samples := d.host.Filesystems(now)
		d.noteFS(samples)
		if err := d.store.WriteFilesystems(ctx, now, samples); err != nil {
			d.log.Warn("writing filesystem sample", "err", err)
		}
		if !wait(ctx, d.bcast, d.cfg.Load().Sampling.FilesystemInterval.D()) {
			return
		}
	}
}

func (d *Daemon) procLoop(ctx context.Context) {
	for ctx.Err() == nil {
		snap, err := d.proc.Collect(time.Now())
		if err != nil {
			d.log.Warn("process collection failed", "err", err)
		} else if err := d.store.WriteProcesses(ctx, snap); err != nil {
			d.log.Warn("writing process sample", "err", err)
		} else {
			d.noteProcs(snap)
		}
		if !wait(ctx, d.bcast, d.cfg.Load().Sampling.ProcessInterval.D()) {
			return
		}
	}
}

func (d *Daemon) maintainLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := d.store.Maintain(ctx, time.Now()); err != nil && ctx.Err() == nil {
			d.log.Warn("maintenance failed", "err", err)
		}
		every := 30 * time.Second
		if s := d.cfg.Load().Rollups.ShortInterval.D(); s > 0 && s < every {
			every = s
		}
		if !wait(ctx, d.bcast, every) {
			return
		}
	}
}

func wait(ctx context.Context, b *broadcast, dly time.Duration) bool {
	if dly < time.Millisecond {
		dly = time.Millisecond
	}
	t := time.NewTimer(dly)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-b.C():
		return ctx.Err() == nil
	case <-t.C:
		return true
	}
}

func (d *Daemon) noteHost(s *collect.HostSample) {
	h := hostLatest(s)
	d.mu.Lock()
	d.latest.SchemaVersion = api.SchemaVersion
	d.latest.CollectedAt = s.Timestamp.UTC().Format(time.RFC3339)
	d.latest.Warm = s.Warm
	d.latest.Host = h
	d.latest.Filesystems = append([]api.FilesystemLatest(nil), d.filesystems...)
	d.latest.ProcessCount = len(d.processes)
	d.latest.TopProcesses = top(d.processes, api.LiveTopN)
	d.live = mustLive(d.latest)
	d.mu.Unlock()
}

func (d *Daemon) noteFS(samples []collect.FilesystemSample) {
	out := make([]api.FilesystemLatest, 0, len(samples))
	for _, s := range samples {
		if s.Unavailable && s.Total == 0 {
			continue
		}
		out = append(out, api.FilesystemLatest{
			Mount: s.Mount, Device: s.Device, FilesystemType: s.FilesystemType,
			TotalBytes: int64(s.Total), UsedBytes: int64(s.Used), AvailableBytes: int64(s.Available),
			UsagePct: round2(s.UsagePct), InodeTotal: int64(s.InodeTotal), InodeUsed: int64(s.InodeUsed),
			InodeUsagePct: round2(s.InodeUsagePct),
		})
	}
	d.mu.Lock()
	d.filesystems = out
	d.latest.Filesystems = append([]api.FilesystemLatest(nil), out...)
	d.live = mustLive(d.latest)
	d.mu.Unlock()
}

func (d *Daemon) noteProcs(snap *collect.ProcessSnapshot) {
	if snap == nil {
		return
	}
	out := make([]api.Process, 0, len(snap.Procs))
	for _, p := range snap.Procs {
		out = append(out, api.Process{
			InstanceID: p.ID(), PID: p.PID, PPID: p.PPID, BootID: p.BootID,
			StarttimeTicks: int64(p.StartTicks), StartedAt: formatUnix(p.StartUnix),
			Name: p.Name, Executable: p.Exe, CommandLine: p.Cmd, CommandLineTruncated: p.CmdTruncated,
			User: p.User, Kernel: p.Kernel,
			CPUUsagePct: round2(p.CPUPercent), RSSBytes: int64(p.RSS), MemoryUsagePct: round2(p.MemPercent),
			ReadBytesPerSec: round2(p.ReadBps), WriteBytesPerSec: round2(p.WriteBps),
			ThreadCount: p.Threads, State: p.State,
		})
	}
	d.mu.Lock()
	d.processes = out
	d.latest.ProcessCount = len(out)
	d.latest.TopProcesses = top(out, api.LiveTopN)
	d.live = mustLive(d.latest)
	d.mu.Unlock()
}

func (d *Daemon) writeSnapshot(s *collect.HostSample) {
	d.mu.Lock()
	fs := append([]api.FilesystemLatest(nil), d.filesystems...)
	d.mu.Unlock()
	var maxPct float64
	var mount string
	for _, f := range fs {
		if f.UsagePct >= maxPct {
			maxPct = f.UsagePct
			mount = f.Mount
		}
	}
	snap := api.Snapshot{
		SchemaVersion:           api.SchemaVersion,
		CollectedAt:             s.Timestamp.UTC().Format(time.RFC3339),
		ExpectedIntervalSeconds: int(d.cfg.Load().Sampling.HostInterval.Seconds()),
		Vitals: api.Vitals{
			CPUUsagePct:     round2(s.CPUUsagePct),
			MemoryUsagePct:  round2(s.MemUsagePct),
			DiskUsagePctMax: round2(maxPct),
			DiskUsageMount:  mount,
			Load1m:          round2(s.Load1),
			CPUCores:        s.CPUCores,
		},
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	raw = append(raw, '\n')
	if err := layout.WriteAtomic(d.paths.Snapshot(), raw, 0o600); err != nil {
		d.log.Warn("writing latest snapshot", "err", err)
	}
}

func (d *Daemon) publish() {
	d.mu.Lock()
	msg := append([]byte(nil), d.live...)
	d.mu.Unlock()
	if len(msg) == 0 {
		return
	}
	d.subMu.Lock()
	defer d.subMu.Unlock()
	kept := d.subs[:0]
	for _, s := range d.subs {
		if s.closed {
			continue
		}
		select {
		case s.ch <- msg:
			kept = append(kept, s)
		default:
			s.closed = true
			close(s.ch)
		}
	}
	d.subs = kept
}

// Reload reads the configuration file and applies it.
func (d *Daemon) Reload() (*config.Config, error) {
	cfg, err := config.Load(d.paths.ConfigFile)
	if err != nil {
		return nil, moniterr.E("config_invalid", err.Error())
	}
	d.apply(cfg)
	d.bcast.Notify()
	return cfg, nil
}

// Subscribe registers a live client. The returned function removes it.
func (d *Daemon) Subscribe(ctx context.Context) (<-chan []byte, func(), error) {
	d.mu.Lock()
	msg := append([]byte(nil), d.live...)
	d.mu.Unlock()
	s := &subscriber{ch: make(chan []byte, 4)}
	d.subMu.Lock()
	if len(d.subs) >= 8 {
		d.subMu.Unlock()
		return nil, nil, moniterr.E("busy", "too many live subscribers")
	}
	d.subs = append(d.subs, s)
	if len(msg) > 0 {
		select {
		case s.ch <- msg:
		default:
		}
	}
	d.subMu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			d.subMu.Lock()
			defer d.subMu.Unlock()
			if s.closed {
				return
			}
			s.closed = true
			close(s.ch)
			kept := d.subs[:0]
			for _, cur := range d.subs {
				if cur != s {
					kept = append(kept, cur)
				}
			}
			d.subs = kept
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return s.ch, cancel, nil
}

// Handle serves one socket request.
func (d *Daemon) Handle(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case "latest":
		return d.Latest(), nil
	case "status":
		return d.Status(ctx)
	case "storage":
		return d.Storage(ctx)
	case "config":
		return ConfigView(d.cfg.Load()), nil
	case "reload":
		cfg, err := d.Reload()
		if err != nil {
			return nil, err
		}
		return ConfigView(cfg), nil
	case "history":
		q, err := historyQuery(req)
		if err != nil {
			return nil, err
		}
		return d.store.History(ctx, q)
	case "process_history":
		q, err := historyQuery(req)
		if err != nil {
			return nil, err
		}
		return d.store.ProcessHistory(ctx, req.InstanceID, q)
	case "process":
		if req.InstanceID == "" {
			return nil, moniterr.E("invalid_argument", "instance id is required")
		}
		if p, ok := d.currentProcess(req.InstanceID); ok && req.At == "" {
			return p, nil
		}
		return d.store.Process(ctx, req.InstanceID)
	case "processes":
		if req.At != "" {
			at, err := time.Parse(time.RFC3339, req.At)
			if err != nil {
				return nil, moniterr.E("invalid_argument", "at must be RFC3339")
			}
			sortKey := req.Sort
			if sortKey == "" {
				sortKey = "cpu"
			}
			limit := req.Limit
			if limit <= 0 {
				limit = 25
			}
			return d.store.ProcessesAt(ctx, at, sortKey, req.Name, limit)
		}
		return d.currentProcesses(req.Sort, req.Name, req.Limit), nil
	default:
		return nil, moniterr.E("invalid_argument", "unknown operation")
	}
}

func (d *Daemon) Latest() api.Latest {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.latest
	out.Filesystems = append([]api.FilesystemLatest(nil), d.latest.Filesystems...)
	out.TopProcesses = append([]api.Process(nil), d.latest.TopProcesses...)
	if out.Filesystems == nil {
		out.Filesystems = []api.FilesystemLatest{}
	}
	if out.TopProcesses == nil {
		out.TopProcesses = []api.Process{}
	}
	return out
}

func (d *Daemon) currentProcesses(sortKey, name string, limit int) api.ProcessList {
	if sortKey == "" {
		sortKey = "cpu"
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	d.mu.Lock()
	procs := append([]api.Process(nil), d.processes...)
	collected := d.latest.CollectedAt
	d.mu.Unlock()
	if name != "" {
		filtered := procs[:0]
		for _, p := range procs {
			if containsFold(p.Name, name) || containsFold(p.CommandLine, name) {
				filtered = append(filtered, p)
			}
		}
		procs = filtered
	}
	sort.SliceStable(procs, func(i, j int) bool {
		switch sortKey {
		case "memory":
			return procs[i].MemoryUsagePct > procs[j].MemoryUsagePct
		case "rss":
			return procs[i].RSSBytes > procs[j].RSSBytes
		case "read":
			return procs[i].ReadBytesPerSec > procs[j].ReadBytesPerSec
		case "write":
			return procs[i].WriteBytesPerSec > procs[j].WriteBytesPerSec
		default:
			return procs[i].CPUUsagePct > procs[j].CPUUsagePct
		}
	})
	if len(procs) > limit {
		procs = procs[:limit]
	}
	if procs == nil {
		procs = []api.Process{}
	}
	return api.ProcessList{SchemaVersion: api.SchemaVersion, CollectedAt: collected, Sort: sortKey, Processes: procs}
}

func (d *Daemon) currentProcess(id string) (api.Process, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.processes {
		if p.InstanceID == id {
			return p, true
		}
	}
	return api.Process{}, false
}

// Status reports daemon health and retention.
func (d *Daemon) Status(ctx context.Context) (api.Status, error) {
	cfg := d.cfg.Load()
	span, err := d.store.Span(ctx)
	if err != nil {
		return api.Status{}, err
	}
	now := time.Now().Unix()
	size := d.store.SizeBytes()
	st := api.Status{
		SchemaVersion:              api.SchemaVersion,
		DaemonState:                "running",
		Version:                    plugin.Version,
		Collecting:                 span.Newest > 0,
		LatestSampleAt:             formatUnix(span.Newest),
		OldestSampleAt:             formatUnix(span.Oldest),
		HostIntervalSeconds:        int(cfg.Sampling.HostInterval.Seconds()),
		ProcessIntervalSeconds:     int(cfg.Sampling.ProcessInterval.Seconds()),
		FilesystemIntervalSeconds:  int(cfg.Sampling.FilesystemInterval.Seconds()),
		ConfiguredRetentionSeconds: cfg.Retention.Maximum.Seconds(),
		DatabaseBytes:              size,
		MaxStorageBytes:            cfg.Storage.MaxSize.Bytes(),
		ProcessInstances:           span.Instances,
		UptimeSeconds:              int64(time.Since(d.started).Seconds()),
	}
	if span.Oldest > 0 {
		st.EffectiveRetentionSeconds = now - span.Oldest
	}
	st.StorageConstrained = store.StorageConstrained(now, span.Oldest, cfg.Retention.Maximum.Seconds(), cfg.Rollups.ArchiveInterval.Seconds(), size, cfg.Storage.MaxSize.Bytes())
	return st, nil
}

// Storage reports the on-disk budget.
func (d *Daemon) Storage(ctx context.Context) (api.StorageStatus, error) {
	st, err := d.Status(ctx)
	if err != nil {
		return api.StorageStatus{}, err
	}
	pct := 0.0
	if st.MaxStorageBytes > 0 {
		pct = round2(float64(st.DatabaseBytes) / float64(st.MaxStorageBytes) * 100)
	}
	return api.StorageStatus{
		SchemaVersion:              api.SchemaVersion,
		DatabaseBytes:              st.DatabaseBytes,
		MaxStorageBytes:            st.MaxStorageBytes,
		PercentUsed:                pct,
		OldestSampleAt:             st.OldestSampleAt,
		NewestSampleAt:             st.LatestSampleAt,
		ConfiguredRetentionSeconds: st.ConfiguredRetentionSeconds,
		EffectiveRetentionSeconds:  st.EffectiveRetentionSeconds,
		StorageConstrained:         st.StorageConstrained,
		Tiers:                      d.store.TierInfos(),
	}, nil
}

// ConfigView converts configuration to the public JSON shape.
func ConfigView(cfg *config.Config) api.ConfigView {
	excl := cfg.Filesystems.Exclude
	if excl == nil {
		excl = []string{}
	}
	return api.ConfigView{
		SchemaVersion:             api.SchemaVersion,
		HostIntervalSeconds:       int(cfg.Sampling.HostInterval.Seconds()),
		ProcessIntervalSeconds:    int(cfg.Sampling.ProcessInterval.Seconds()),
		FilesystemIntervalSeconds: int(cfg.Sampling.FilesystemInterval.Seconds()),
		RetentionRawSeconds:       cfg.Retention.Raw.Seconds(),
		RetentionShortSeconds:     cfg.Retention.Short.Seconds(),
		RetentionMediumSeconds:    cfg.Retention.Medium.Seconds(),
		RetentionLongSeconds:      cfg.Retention.Long.Seconds(),
		RetentionMaximumSeconds:   cfg.Retention.Maximum.Seconds(),
		RollupShortSeconds:        cfg.Rollups.ShortInterval.Seconds(),
		RollupMediumSeconds:       cfg.Rollups.MediumInterval.Seconds(),
		RollupLongSeconds:         cfg.Rollups.LongInterval.Seconds(),
		RollupArchiveSeconds:      cfg.Rollups.ArchiveInterval.Seconds(),
		MaxStorageBytes:           cfg.Storage.MaxSize.Bytes(),
		CommandLineMaxBytes:       cfg.Process.CommandLineMaxBytes,
		FilesystemTimeoutSeconds:  int(cfg.Filesystems.Timeout.Seconds()),
		FilesystemExclude:         excl,
	}
}

func historyQuery(req ipc.Request) (store.HistoryQuery, error) {
	from, err := time.Parse(time.RFC3339, req.From)
	if err != nil {
		return store.HistoryQuery{}, moniterr.E("invalid_argument", "from must be RFC3339")
	}
	to, err := time.Parse(time.RFC3339, req.To)
	if err != nil {
		return store.HistoryQuery{}, moniterr.E("invalid_argument", "to must be RFC3339")
	}
	max := req.MaxPoints
	if max == 0 {
		max = api.DefaultMaxPoints
	}
	return store.HistoryQuery{From: from.UTC(), To: to.UTC(), Max: max, Cursor: req.Cursor}, nil
}

func hostLatest(s *collect.HostSample) api.HostLatest {
	return api.HostLatest{
		CPUUsagePct: round2(s.CPUUsagePct), CPUUserPct: round2(s.CPUUserPct),
		CPUSystemPct: round2(s.CPUSystemPct), CPUIOWaitPct: round2(s.CPUIOWaitPct),
		CPUStealPct: round2(s.CPUStealPct), CPUCores: s.CPUCores,
		MemoryTotalBytes: int64(s.MemTotal), MemoryUsedBytes: int64(s.MemUsed),
		MemoryAvailableBytes: int64(s.MemAvailable), MemoryUsagePct: round2(s.MemUsagePct),
		SwapTotalBytes: int64(s.SwapTotal), SwapUsedBytes: int64(s.SwapUsed), SwapUsagePct: round2(s.SwapUsagePct),
		Load1m: round2(s.Load1), Load5m: round2(s.Load5), Load15m: round2(s.Load15),
		UptimeSeconds:       int64(s.UptimeSeconds),
		DiskReadBytesPerSec: round2(s.DiskReadBps), DiskWriteBytesPerSec: round2(s.DiskWriteBps),
		NetworkReceiveBytesPerSec: round2(s.NetRxBps), NetworkTransmitBytesPerSec: round2(s.NetTxBps),
	}
}

func mustLive(latest api.Latest) []byte {
	msg := api.LiveSample{
		Type: "sample", SchemaVersion: api.SchemaVersion, Timestamp: latest.CollectedAt,
		Host: latest.Host, Filesystems: latest.Filesystems, ProcessCount: latest.ProcessCount,
		TopProcesses: latest.TopProcesses,
	}
	if msg.Filesystems == nil {
		msg.Filesystems = []api.FilesystemLatest{}
	}
	if msg.TopProcesses == nil {
		msg.TopProcesses = []api.Process{}
	}
	b, _ := json.Marshal(msg)
	return b
}

func top(procs []api.Process, n int) []api.Process {
	cp := append([]api.Process(nil), procs...)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].CPUUsagePct > cp[j].CPUUsagePct })
	if len(cp) > n {
		cp = cp[:n]
	}
	if cp == nil {
		cp = []api.Process{}
	}
	return cp
}

func formatUnix(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
