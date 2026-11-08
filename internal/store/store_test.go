package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/collect"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/testproc"
)

func TestOpenWALAndReopen(t *testing.T) {
	db := openDB(t, defaultTiers())
	var mode, sync string
	var version int
	if err := db.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" || sync != "2" || version != 1 {
		t.Fatalf("mode=%s synchronous=%s user_version=%d", mode, sync, version)
	}
	info, err := os.Stat(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, defaultTiers())
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if err := again.sql.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("reopen version %d %v", version, err)
	}
}

func TestRollupAveragesAndWeightedRate(t *testing.T) {
	tiers := defaultTiers()
	tiers.RawRetain = 30
	tiers.Short = 15
	tiers.ShortRetain = 3600
	db := openDB(t, tiers)
	ctx := context.Background()
	base := time.Unix(1000, 0)
	writeHost(t, db, base, 10, 1000, 5*time.Second, 200)
	writeHost(t, db, base.Add(4*time.Second), 40, 1000, time.Second, 1000)
	if err := db.Maintain(ctx, time.Unix(1050, 0)); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.DebugResCount(ctx, "host_samples", 0, 1000); n != 0 {
		t.Fatalf("raw row survived rollup: %d", n)
	}
	hist, err := db.History(ctx, HistoryQuery{From: time.Unix(990, 0), To: time.Unix(1005, 0), Max: 10})
	if err != nil {
		t.Fatal(err)
	}
	if hist.ResolutionSeconds != 15 || len(hist.Host) != 1 {
		t.Fatalf("history = %+v", hist)
	}
	p := hist.Host[0]
	if p.SampleCount != 2 || p.CPUUsagePctAvg != 25 || p.CPUUsagePctMax != 40 {
		t.Fatalf("cpu avg/max/count = %v %v %d", p.CPUUsagePctAvg, p.CPUUsagePctMax, p.SampleCount)
	}
	if p.DiskReadBytesPerSecAvg < 333.3 || p.DiskReadBytesPerSecAvg > 333.4 || p.DiskReadBytesPerSecMax != 1000 {
		t.Fatalf("rate avg/max = %v %v, want about 333.33 and 1000", p.DiskReadBytesPerSecAvg, p.DiskReadBytesPerSecMax)
	}
}

func TestCascadeTiersRetentionAndCeiling(t *testing.T) {
	tiers := defaultTiers()
	tiers.MaxBytes = 1 << 40
	db := openDB(t, tiers)
	ctx := context.Background()
	now := time.Unix(20_000_000, 0)
	ages := []time.Duration{
		30 * time.Minute,
		2 * time.Hour,
		12 * time.Hour,
		2 * 24 * time.Hour,
		10 * 24 * time.Hour,
		100 * 24 * time.Hour,
	}
	for _, age := range ages {
		writeHost(t, db, now.Add(-age), 10, 0, time.Second, 0)
	}
	if err := db.Maintain(ctx, now); err != nil {
		t.Fatal(err)
	}
	assertRes(t, db, now.Add(-30*time.Minute), 0)
	assertRes(t, db, now.Add(-2*time.Hour), 15)
	assertRes(t, db, now.Add(-12*time.Hour), 60)
	assertRes(t, db, now.Add(-2*24*time.Hour), 300)
	assertRes(t, db, now.Add(-10*24*time.Hour), 3600)
	var ancient int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM host_samples WHERE ts < ?`, now.Add(-90*24*time.Hour).Unix()).Scan(&ancient); err != nil {
		t.Fatal(err)
	}
	if ancient != 0 {
		t.Fatalf("samples older than maximum retention survived: %d", ancient)
	}

	// The ceiling deletes aged rows and keeps the most recent sampling window.
	writeHost(t, db, now.Add(-3*time.Second), 5, 0, time.Second, 0)
	tiers.MaxBytes = 4096
	db.SetTiers(tiers)
	if err := db.Maintain(ctx, now); err != nil {
		t.Fatal(err)
	}
	if n, err := db.DebugResCount(ctx, "host_samples", 0, now.Add(-3*time.Second).Unix()); err != nil || n != 1 {
		t.Fatalf("recent sample was deleted: count %d err %v", n, err)
	}
	if n, _ := db.DebugResCount(ctx, "host_samples", 3600, align(now.Add(-10*24*time.Hour).Unix(), 3600)); n != 0 {
		t.Fatalf("ceiling left a 10d sample in place: %d", n)
	}
	if !StorageConstrained(now.Unix(), now.Add(-time.Hour).Unix(), tiers.MaxRetain, tiers.Archive, tiers.MaxBytes, tiers.MaxBytes) {
		t.Fatal("a full database younger than the horizon should be constrained")
	}
	if StorageConstrained(now.Unix(), now.Add(-time.Minute).Unix(), tiers.MaxRetain, tiers.Archive, 100, tiers.MaxBytes) {
		t.Fatal("a young database under the ceiling is not constrained")
	}
}

func TestHistoryPagesGapsAndCursorExpiry(t *testing.T) {
	tiers := defaultTiers()
	tiers.RawInterval = 5
	tiers.Short = 10
	tiers.Medium = 20
	tiers.Long = 20
	tiers.Archive = 20
	db := openDB(t, tiers)
	ctx := context.Background()
	base := time.Unix(2_000_000, 0)
	for i := 0; i < 40; i++ {
		if i == 7 {
			continue
		}
		writeHost(t, db, base.Add(time.Duration(i)*5*time.Second), float64(i), 0, time.Second, 0)
		if err := db.WriteFilesystems(ctx, base.Add(time.Duration(i)*5*time.Second), []collect.FilesystemSample{{
			Mount: "/", Device: "/dev/sda1", FilesystemType: "ext4",
			Total: 100, Used: 40, Available: 50, UsagePct: 40,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	q := HistoryQuery{From: base, To: base.Add(200 * time.Second), Max: 3}
	page, err := db.History(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if page.ResolutionSeconds != 20 || page.NextCursor == nil || len(page.Host) == 0 {
		t.Fatalf("page = res %d cursor %v points %d", page.ResolutionSeconds, page.NextCursor, len(page.Host))
	}
	if len(page.Filesystems) != 1 || page.Filesystems[0].Mount != "/" || len(page.Filesystems[0].Points) == 0 {
		t.Fatalf("filesystem page = %+v", page.Filesystems)
	}
	if page.Filesystems[0].Points[0].Timestamp < page.From || page.Filesystems[0].Points[0].Timestamp >= page.To {
		t.Fatalf("filesystem point %s outside host page %s..%s", page.Filesystems[0].Points[0].Timestamp, page.From, page.To)
	}
	q.Cursor = *page.NextCursor
	next, err := db.History(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if next.ResolutionSeconds != page.ResolutionSeconds || next.From != page.To {
		t.Fatalf("next page from %s res %d, previous to %s res %d", next.From, next.ResolutionSeconds, page.To, page.ResolutionSeconds)
	}
	// i==7 (base+35s) was not stored. Its 20s bucket still exists from the
	// neighbouring samples, with three samples rather than a filled fourth.
	var gapCount int
	gapTS := base.Add(20 * time.Second).UTC().Format(time.RFC3339)
	for _, p := range page.Host {
		if p.Timestamp == gapTS {
			gapCount = p.SampleCount
		}
	}
	if gapCount != 3 {
		t.Fatalf("gap bucket %s sample_count = %d, want 3 (one missing sample, no fabricated point)", gapTS, gapCount)
	}

	if _, err := db.sql.Exec(`DELETE FROM host_samples`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.History(ctx, q); err == nil || moniterr.Code(err) != "cursor_expired" {
		t.Fatalf("expired cursor = %v", err)
	}

	wide := HistoryQuery{From: base, To: base.Add(time.Hour), Max: 500}
	// Two mounts make 500 points exceed the payload budget.
	if err := db.WriteFilesystems(ctx, base, []collect.FilesystemSample{
		{Mount: "/a", Device: "/dev/a", FilesystemType: "ext4", Total: 10, Used: 1, Available: 9, UsagePct: 10},
		{Mount: "/b", Device: "/dev/b", FilesystemType: "xfs", Total: 10, Used: 1, Available: 9, UsagePct: 10},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.History(ctx, wide); err == nil || moniterr.Code(err) != "invalid_argument" {
		t.Fatalf("payload cap = %v", err)
	}
	if page.AvailableFrom == "" || page.AvailableTo == "" {
		t.Fatal("available range was empty")
	}
}

func TestProcessAttribution(t *testing.T) {
	db := openDB(t, defaultTiers())
	ctx := context.Background()
	when := time.Unix(1_800_000_000, 0)
	old := proc("boot-a", 42, 1000, "nginx-old")
	neu := proc("boot-a", 42, 5000, "nginx-new")
	for _, p := range []collect.Proc{old, neu} {
		if err := db.WriteProcesses(ctx, &collect.ProcessSnapshot{Timestamp: when, Procs: []collect.Proc{p}}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := db.ProcessesAt(ctx, when, "cpu", "", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Processes) != 2 || list.Processes[0].InstanceID == list.Processes[1].InstanceID {
		t.Fatalf("pid reuse merged: %+v", list.Processes)
	}
	short := collect.Instance{Identity: collect.Identity{BootID: "boot-a", PID: 7, StartTicks: 9}, Name: "curl", Cmd: "curl example"}
	if err := db.WriteProcesses(ctx, &collect.ProcessSnapshot{
		Timestamp: when,
		First:     []collect.Instance{short},
		Exited:    []collect.Identity{short.Identity},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Process(ctx, short.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exited || got.Name != "curl" {
		t.Fatalf("short-lived process = %+v", got)
	}
	// Exited processes stay queryable. Mark nginx-old exited and read it back.
	if err := db.WriteProcesses(ctx, &collect.ProcessSnapshot{
		Timestamp: when.Add(time.Second),
		Exited:    []collect.Identity{old.Identity},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = db.Process(ctx, old.ID())
	if err != nil || !got.Exited {
		t.Fatalf("exited history = %+v %v", got, err)
	}
}

func TestResourceProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("resource profile")
	}
	for _, n := range []int{25, 100, 250, 500, 1000} {
		tree := testproc.New(t)
		for i := 0; i < n; i++ {
			tree.SetProc(testproc.Proc{
				PID: i + 1, PPID: 1, Comm: "worker", State: "S",
				UTime: 10, STime: 5, Threads: 2, StartTicks: 1000, RSSPages: 50,
				Cmdline: "worker --job", ReadBytes: 100, WriteBytes: 100,
			})
		}
		collector := collect.NewProcess(tree.Proc, 100)
		when := time.Unix(1_900_000_000, 0)
		if _, err := collector.Collect(when); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			tree.SetProc(testproc.Proc{
				PID: i + 1, PPID: 1, Comm: "worker", State: "R",
				UTime: 20, STime: 5, Threads: 2, StartTicks: 1000, RSSPages: 50,
				Cmdline: "worker --job", ReadBytes: 200, WriteBytes: 150,
			})
		}
		start := time.Now()
		snap, err := collector.Collect(when.Add(time.Second))
		collected := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Procs) != n {
			t.Fatalf("collected %d of %d", len(snap.Procs), n)
		}

		db := openDB(t, defaultTiers())
		ctx := context.Background()
		start = time.Now()
		if err := db.WriteProcesses(ctx, snap); err != nil {
			t.Fatal(err)
		}
		write := time.Since(start)
		start = time.Now()
		list, err := db.ProcessesAt(ctx, when.Add(time.Second), "cpu", "", 25)
		if err != nil {
			t.Fatal(err)
		}
		query := time.Since(start)
		if len(list.Processes) == 0 {
			t.Fatal("empty process page")
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		t.Logf("os=%s arch=%s processes=%d collect=%s write=%s query=%s db_bytes=%d heap_inuse=%d",
			runtime.GOOS, runtime.GOARCH, n, collected, write, query, db.SizeBytes(), mem.HeapInuse)
	}
}

func defaultTiers() Tiers {
	return Tiers{
		RawInterval: 5, RawRetain: 3600,
		Short: 15, ShortRetain: 6 * 3600,
		Medium: 60, MediumRetain: 24 * 3600,
		Long: 300, LongRetain: 7 * 24 * 3600,
		Archive: 3600, MaxRetain: 90 * 24 * 3600,
		MaxBytes: 1000 * 1000 * 1000,
	}
}

func openDB(t *testing.T, tiers Tiers) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "metrics.db"), tiers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeHost(t *testing.T, db *DB, at time.Time, cpu float64, diskBytes uint64, elapsed time.Duration, bps float64) {
	t.Helper()
	err := db.WriteHost(context.Background(), collect.HostSample{
		Timestamp: at, Warm: true, Elapsed: elapsed,
		CPUUsagePct: cpu, CPUCores: 4,
		MemTotal: 100, MemUsed: 40, MemAvailable: 60, MemUsagePct: 40,
		DiskReadBytes: diskBytes, DiskReadBps: bps,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertRes(t *testing.T, db *DB, at time.Time, res int64) {
	t.Helper()
	width := res
	if width == 0 {
		width = 1
	}
	ts := at.Unix()
	if res > 0 {
		ts = ts - ts%res
	}
	n, err := db.DebugResCount(context.Background(), "host_samples", res, ts)
	if err != nil || n != 1 {
		t.Fatalf("sample %s res %d ts %d count %d err %v", at, res, ts, n, err)
	}
}

func align(ts, width int64) int64 {
	if width <= 1 {
		return ts
	}
	return ts - ts%width
}

func proc(boot string, pid int, start uint64, name string) collect.Proc {
	return collect.Proc{
		Instance: collect.Instance{
			Identity: collect.Identity{BootID: boot, PID: pid, StartTicks: start},
			Name:     name, Cmd: name, StartUnix: 100, PPID: 1,
		},
		Metrics: collect.Metrics{CPUPercent: float64(pid), RSS: 1000, MemPercent: 1, Threads: 1, State: "S", Elapsed: time.Second},
	}
}
