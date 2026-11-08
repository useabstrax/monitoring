package collect

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/testproc"
)

func TestHostCounters(t *testing.T) {
	tree := testproc.New(t)
	base := testproc.CPUTimes{User: 1000, System: 500, Idle: 8000, IOWait: 100, IRQ: 50, SoftIRQ: 50, Steal: 20}
	tree.SetStat(testproc.CPUStat{
		Total:        base,
		Cores:        []testproc.CPUTimes{base, base},
		BootTimeUnix: 1754000000,
	})
	tree.SetDiskStats(10, 1000, 10, 1000)
	tree.SetNetDev(10_000, 20_000)

	h := NewHost(tree.Proc, tree.Sys)
	t0 := time.Unix(1_800_000_000, 0)
	first, err := h.Collect(t0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Warm {
		t.Fatal("first sample is a baseline and must not report rates")
	}
	if first.CPUCores != 2 {
		t.Fatalf("cores = %d", first.CPUCores)
	}
	if first.MemTotal != 16*1024*1024*1024 {
		t.Fatalf("mem total = %d", first.MemTotal)
	}
	if first.MemUsagePct < 49.9 || first.MemUsagePct > 50.1 {
		t.Fatalf("mem usage = %v", first.MemUsagePct)
	}
	if first.SwapUsagePct < 49.9 || first.SwapUsagePct > 50.1 {
		t.Fatalf("swap usage = %v", first.SwapUsagePct)
	}
	if first.Load1 != 1.52 || first.Load15 != 0.71 {
		t.Fatalf("load = %v %v", first.Load1, first.Load15)
	}
	if first.UptimeSeconds < 864000 || first.UptimeSeconds > 864001 {
		t.Fatalf("uptime = %v", first.UptimeSeconds)
	}

	next := base
	next.User += 50
	next.System += 30
	next.Idle += 400
	next.IOWait += 10
	next.Steal += 10
	tree.SetStat(testproc.CPUStat{Total: next, Cores: []testproc.CPUTimes{next, next}, BootTimeUnix: 1754000000})
	tree.SetDiskStats(10, 1010, 10, 1004)
	tree.SetNetDev(12_000, 20_500)

	second, err := h.Collect(t0.Add(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Warm {
		t.Fatal("second sample should be warm")
	}
	assertNear(t, "cpu", second.CPUUsagePct, 18)
	assertNear(t, "user", second.CPUUserPct, 10)
	assertNear(t, "system", second.CPUSystemPct, 6)
	assertNear(t, "iowait", second.CPUIOWaitPct, 2)
	assertNear(t, "steal", second.CPUStealPct, 2)
	if second.CPUUsagePct > 100 {
		t.Fatalf("host cpu must stay within 0-100, got %v", second.CPUUsagePct)
	}
	// 10 sectors * 512 / 2s, and the partition and loop device are not added.
	assertNear(t, "disk read", second.DiskReadBps, 2560)
	assertNear(t, "disk write", second.DiskWriteBps, 1024)
	assertNear(t, "net rx", second.NetRxBps, 1000)
	assertNear(t, "net tx", second.NetTxBps, 250)
}

func TestFilesystemsFilterAndInodes(t *testing.T) {
	tree := testproc.New(t)
	h := NewHost(tree.Proc, tree.Sys)
	h.probeSpawn = func(fn func()) { fn() }
	h.Statfs = func(path string) (Capacity, error) {
		return Capacity{Total: 1000, Used: 400, Available: 500, InodeTotal: 100, InodeUsed: 25}, nil
	}
	// The probe cache is built on first use from the Statfs field.
	h.probes = nil
	samples := h.Filesystems(time.Now())
	got := map[string]FilesystemSample{}
	for _, s := range samples {
		got[s.Mount] = s
	}
	if _, ok := got["/"]; !ok {
		t.Fatalf("missing / in %#v", samples)
	}
	if _, ok := got["/var"]; !ok {
		t.Fatalf("missing /var in %#v", samples)
	}
	for _, skip := range []string{"/proc", "/run", "/var/lib/docker/overlay2"} {
		if _, ok := got[skip]; ok {
			t.Fatalf("pseudo mount %s was collected", skip)
		}
	}
	root := got["/"]
	if root.InodeTotal != 100 || root.InodeUsed != 25 {
		t.Fatalf("inodes = %d %d", root.InodeTotal, root.InodeUsed)
	}
	if root.UsagePct < 39 || root.UsagePct > 41 {
		t.Fatalf("usage = %v (used is total-free, not total-available)", root.UsagePct)
	}

	h.probes = nil
	h.SetExclude([]string{"/var"})
	samples = h.Filesystems(time.Now())
	for _, s := range samples {
		if s.Mount == "/var" {
			t.Fatal("exclude pattern did not drop /var")
		}
	}
}

func TestProcessIdentityCPUAndExit(t *testing.T) {
	tree := testproc.New(t)
	c := NewProcess(tree.Proc, 100)
	c.SetMemTotal(2 * uint64(os.Getpagesize()) * 100)
	c.SetMaxCommandLine(32)
	cmd := "python3 -c " + strings.Repeat("α", 40)
	tree.SetProc(testproc.Proc{
		PID: 42, PPID: 1, Comm: "python3", State: "R",
		UTime: 100, STime: 50, Threads: 4, StartTicks: 5000, RSSPages: 100,
		Cmdline: cmd, Exe: "/usr/bin/python3",
		ReadBytes: 1000, WriteBytes: 2000, UID: os.Getuid(),
	})
	t0 := time.Unix(1_800_000_100, 0)
	first, err := c.Collect(t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Procs) != 0 || len(first.First) != 1 {
		t.Fatalf("first sight = procs %d first %d", len(first.Procs), len(first.First))
	}
	id := first.First[0].ID()
	if first.First[0].CmdTruncated != true || len(first.First[0].Cmd) > 32 {
		t.Fatalf("command line was not truncated: %q", first.First[0].Cmd)
	}
	if strings.Contains(first.First[0].Cmd, "\x00") {
		t.Fatal("command line kept NUL separators")
	}

	tree.SetProc(testproc.Proc{
		PID: 42, PPID: 1, Comm: "python3", State: "R",
		UTime: 400, STime: 50, Threads: 4, StartTicks: 5000, RSSPages: 100,
		Cmdline: cmd, Exe: "/usr/bin/python3",
		ReadBytes: 6000, WriteBytes: 4000, UID: os.Getuid(),
	})
	second, err := c.Collect(t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Procs) != 1 {
		t.Fatalf("procs = %d", len(second.Procs))
	}
	p := second.Procs[0]
	if p.ID() != id {
		t.Fatalf("instance id changed: %s %s", id, p.ID())
	}
	assertNear(t, "process cpu", p.CPUPercent, 300)
	if p.CPUPercent <= 100 {
		t.Fatal("process cpu should be allowed to exceed 100")
	}
	if p.RSS != uint64(os.Getpagesize())*100 {
		t.Fatalf("rss = %d", p.RSS)
	}
	assertNear(t, "mem", p.MemPercent, 50)
	assertNear(t, "read", p.ReadBps, 5000)
	assertNear(t, "write", p.WriteBps, 2000)
	if p.Threads != 4 || p.State != "R" {
		t.Fatalf("threads/state = %d %s", p.Threads, p.State)
	}
	if p.StartUnix != 1754000000+50 {
		t.Fatalf("started = %d", p.StartUnix)
	}

	tree.RemoveProc(42)
	gone, err := c.Collect(t0.Add(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(gone.Exited) != 1 || gone.Exited[0].ID() != id {
		t.Fatalf("exit = %+v", gone.Exited)
	}

	// PID reuse is a new instance.
	tree.SetProc(testproc.Proc{
		PID: 42, PPID: 1, Comm: "nginx", State: "S",
		StartTicks: 9000, RSSPages: 10, Cmdline: "nginx",
	})
	reused, err := c.Collect(t0.Add(3 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(reused.First) != 1 || reused.First[0].ID() == id {
		t.Fatalf("pid reuse merged history: %+v", reused.First)
	}

	tree.SetBootID("other-boot")
	tree.SetProc(testproc.Proc{
		PID: 42, PPID: 1, Comm: "nginx", State: "S",
		StartTicks: 9000, RSSPages: 10, Cmdline: "nginx",
	})
	booted, err := c.Collect(t0.Add(4 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	foundOld := false
	for _, ex := range booted.Exited {
		if ex.ID() == reused.First[0].ID() {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("reboot did not retire the previous instance: %+v", booted.Exited)
	}
	if len(booted.First) != 1 || booted.First[0].BootID != "other-boot" {
		t.Fatalf("new boot instance = %+v", booted.First)
	}
}

func TestKernelThread(t *testing.T) {
	tree := testproc.New(t)
	tree.SetProc(testproc.Proc{PID: 2, Comm: "kthreadd", Kernel: true, StartTicks: 1})
	c := NewProcess(tree.Proc, 100)
	snap, err := c.Collect(time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.First) != 1 || !snap.First[0].Kernel || snap.First[0].Cmd != "[kthreadd]" {
		t.Fatalf("kernel thread = %+v", snap.First)
	}
}

func assertNear(t *testing.T, name string, got, want float64) {
	t.Helper()
	if got < want-0.05 || got > want+0.05 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
