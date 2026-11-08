package collect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HostSample is one host reading. Counter-derived fields need two readings,
// so the first sample has Warm set to false and those fields zero.
type HostSample struct {
	Timestamp time.Time
	Warm      bool
	Elapsed   time.Duration

	CPUUsagePct  float64
	CPUUserPct   float64
	CPUSystemPct float64
	CPUIOWaitPct float64
	CPUStealPct  float64
	CPUCores     int

	MemTotal     uint64
	MemUsed      uint64
	MemAvailable uint64
	MemUsagePct  float64
	SwapTotal    uint64
	SwapUsed     uint64
	SwapUsagePct float64

	Load1  float64
	Load5  float64
	Load15 float64

	UptimeSeconds float64

	DiskReadBytes  uint64
	DiskWriteBytes uint64
	DiskReadBps    float64
	DiskWriteBps   float64
	NetRxBytes     uint64
	NetTxBytes     uint64
	NetRxBps       float64
	NetTxBps       float64
}

// FilesystemSample is capacity for one mount.
type FilesystemSample struct {
	Mount          string
	Device         string
	FilesystemType string
	Total          uint64
	Used           uint64
	Available      uint64
	UsagePct       float64
	InodeTotal     uint64
	InodeUsed      uint64
	InodeUsagePct  float64
	Unavailable    bool
}

type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuTimes) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

// busy excludes idle and iowait. A CPU waiting on I/O is not doing work.
func (c cpuTimes) busy() uint64 {
	return c.user + c.nice + c.system + c.irq + c.softirq + c.steal
}

type diskCounters struct {
	readsCompleted, sectorsRead, writesCompleted, sectorsWritten uint64
}

type netCounters struct{ rxBytes, txBytes uint64 }

type snapshot struct {
	when    time.Time
	cpu     cpuTimes
	cpuCore []cpuTimes
	disk    diskCounters
	net     netCounters
}

// StatfsFunc reports capacity for a mount. Tests supply it.
type StatfsFunc func(path string) (Capacity, error)

// Host collects host metrics. It is not safe for concurrent Collect calls.
type Host struct {
	ProcRoot string
	SysRoot  string
	Statfs   StatfsFunc
	// MountFilter overrides the default pseudo-filesystem filter.
	MountFilter func(Mount) bool
	// Exclude patterns are applied after the default filter.
	Exclude []string

	statfsTimeout time.Duration
	prev          *snapshot
	sectorSize    uint64
	probes        *mountProbes
	probeSpawn    func(func())
}

// NewHost returns a collector rooted at procRoot and sysRoot.
func NewHost(procRoot, sysRoot string) *Host {
	return &Host{
		ProcRoot:      procRoot,
		SysRoot:       sysRoot,
		Statfs:        platformStatfs,
		sectorSize:    512,
		statfsTimeout: 10 * time.Second,
	}
}

// SetStatfsTimeout changes how long a filesystem may take to answer.
func (h *Host) SetStatfsTimeout(d time.Duration) {
	if d > 0 {
		h.statfsTimeout = d
	}
}

// SetExclude replaces filesystem exclude patterns.
func (h *Host) SetExclude(patterns []string) { h.Exclude = append([]string(nil), patterns...) }

// Collect reads host counters. The first call primes the baseline.
func (h *Host) Collect(now time.Time) (*HostSample, error) {
	snap, err := h.readSnapshot(now)
	if err != nil {
		return nil, err
	}
	s := &HostSample{Timestamp: now, CPUCores: len(snap.cpuCore)}
	if s.CPUCores == 0 {
		s.CPUCores = 1
	}
	if err := h.fillMemory(s); err != nil {
		return nil, err
	}
	h.fillLoad(s)
	h.fillUptime(s)
	if h.prev != nil {
		elapsed := snap.when.Sub(h.prev.when)
		if elapsed > 0 {
			s.Warm = true
			s.Elapsed = elapsed
			h.fillCPU(s, snap)
			h.fillRates(s, snap, elapsed.Seconds())
		}
	}
	h.prev = snap
	return s, nil
}

// Reset drops the counter baseline.
func (h *Host) Reset() { h.prev = nil }

// Filesystems reads mount capacity. It does not call statfs on the caller's
// goroutine; a hung mount costs at most one blocked probe.
func (h *Host) Filesystems(now time.Time) []FilesystemSample {
	filter := h.mountFilter()
	probes := h.mountProbes()
	live := map[string]bool{}
	seenDevice := map[string]bool{}
	var out []FilesystemSample
	for _, m := range h.Mounts() {
		if !filter(m) {
			continue
		}
		if seenDevice[m.Device] {
			continue
		}
		live[m.Point] = true
		r := probes.check(m.Point, now)
		d := FilesystemSample{Mount: m.Point, Device: m.Device, FilesystemType: m.FSType}
		switch {
		case r.Hung:
			d.Unavailable = true
			if r.Have && r.Total > 0 {
				d.Total = r.Total
				d.Used = r.Used
				d.Available = r.Available
				d.UsagePct = usagePct(r.Used, r.Total)
				d.InodeTotal = r.InodeTotal
				d.InodeUsed = r.InodeUsed
				d.InodeUsagePct = usagePct(r.InodeUsed, r.InodeTotal)
			} else {
				continue
			}
		case r.Have && r.Total > 0:
			d.Total = r.Total
			d.Used = r.Used
			d.Available = r.Available
			d.UsagePct = usagePct(r.Used, r.Total)
			d.InodeTotal = r.InodeTotal
			d.InodeUsed = r.InodeUsed
			d.InodeUsagePct = usagePct(r.InodeUsed, r.InodeTotal)
		default:
			continue
		}
		seenDevice[m.Device] = true
		out = append(out, d)
	}
	probes.retain(live)
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

func (h *Host) mountProbes() *mountProbes {
	if h.probes == nil {
		h.probes = newMountProbes(h.Statfs, h.statfsTimeout, h.probeSpawn)
	}
	return h.probes
}

func (h *Host) mountFilter() func(Mount) bool {
	if h.MountFilter != nil {
		return h.MountFilter
	}
	patterns := h.Exclude
	return func(m Mount) bool {
		if !defaultMountFilter(m) {
			return false
		}
		for _, pat := range patterns {
			for _, field := range []string{m.Point, m.Device, m.FSType} {
				if ok, err := filepath.Match(pat, field); err == nil && ok {
					return false
				}
			}
		}
		return true
	}
}

func (h *Host) readSnapshot(now time.Time) (*snapshot, error) {
	snap := &snapshot{when: now}
	if err := h.readStat(snap); err != nil {
		return nil, err
	}
	h.readDiskstats(snap)
	h.readNetDev(snap)
	return snap, nil
}

func (h *Host) readStat(snap *snapshot) error {
	path := join(h.ProcRoot, "stat")
	err := eachLine(path, func(line string) error {
		if !strings.HasPrefix(line, "cpu") {
			return nil
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			return nil
		}
		t := cpuTimes{
			user: parseUint(fields[1]), nice: parseUint(fields[2]),
			system: parseUint(fields[3]), idle: parseUint(fields[4]),
			iowait: parseUint(fields[5]), irq: parseUint(fields[6]),
			softirq: parseUint(fields[7]),
		}
		if len(fields) > 8 {
			t.steal = parseUint(fields[8])
		}
		if fields[0] == "cpu" {
			snap.cpu = t
		} else {
			snap.cpuCore = append(snap.cpuCore, t)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if snap.cpu.total() == 0 {
		return fmt.Errorf("read %s: no aggregate cpu line", path)
	}
	return nil
}

func (h *Host) fillCPU(s *HostSample, snap *snapshot) {
	prev := h.prev
	totalDelta := float64(snap.cpu.total() - prev.cpu.total())
	if totalDelta <= 0 {
		return
	}
	pct := func(now, before uint64) float64 {
		if now < before {
			return 0
		}
		return float64(now-before) / totalDelta * 100
	}
	busyDelta := float64(0)
	if snap.cpu.busy() >= prev.cpu.busy() {
		busyDelta = float64(snap.cpu.busy() - prev.cpu.busy())
	}
	s.CPUUsagePct = clamp(busyDelta/totalDelta*100, 0, 100)
	s.CPUUserPct = pct(snap.cpu.user+snap.cpu.nice, prev.cpu.user+prev.cpu.nice)
	s.CPUSystemPct = pct(snap.cpu.system+snap.cpu.irq+snap.cpu.softirq, prev.cpu.system+prev.cpu.irq+prev.cpu.softirq)
	s.CPUIOWaitPct = pct(snap.cpu.iowait, prev.cpu.iowait)
	s.CPUStealPct = pct(snap.cpu.steal, prev.cpu.steal)
}

func (h *Host) fillRates(s *HostSample, snap *snapshot, elapsed float64) {
	if elapsed <= 0 {
		return
	}
	prev := h.prev
	s.DiskReadBytes = delta(snap.disk.sectorsRead, prev.disk.sectorsRead) * h.sectorSize
	s.DiskWriteBytes = delta(snap.disk.sectorsWritten, prev.disk.sectorsWritten) * h.sectorSize
	s.NetRxBytes = delta(snap.net.rxBytes, prev.net.rxBytes)
	s.NetTxBytes = delta(snap.net.txBytes, prev.net.txBytes)
	s.DiskReadBps = float64(s.DiskReadBytes) / elapsed
	s.DiskWriteBps = float64(s.DiskWriteBytes) / elapsed
	s.NetRxBps = float64(s.NetRxBytes) / elapsed
	s.NetTxBps = float64(s.NetTxBytes) / elapsed
}

func delta(now, before uint64) uint64 {
	if now < before {
		return 0
	}
	return now - before
}

func (h *Host) fillMemory(s *HostSample) error {
	path := join(h.ProcRoot, "meminfo")
	vals := map[string]uint64{}
	err := eachLine(path, func(line string) error {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			return nil
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil
		}
		n := parseUint(fields[0])
		if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
			n *= 1024
		}
		vals[key] = n
		return nil
	})
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	s.MemTotal = vals["MemTotal"]
	s.MemAvailable = vals["MemAvailable"]
	if s.MemAvailable == 0 {
		s.MemAvailable = vals["MemFree"] + vals["Buffers"] + vals["Cached"] + vals["SReclaimable"]
	}
	if s.MemTotal >= s.MemAvailable {
		s.MemUsed = s.MemTotal - s.MemAvailable
	}
	s.MemUsagePct = usagePct(s.MemUsed, s.MemTotal)
	s.SwapTotal = vals["SwapTotal"]
	if s.SwapTotal >= vals["SwapFree"] {
		s.SwapUsed = s.SwapTotal - vals["SwapFree"]
	}
	s.SwapUsagePct = usagePct(s.SwapUsed, s.SwapTotal)
	return nil
}

func (h *Host) fillLoad(s *HostSample) {
	fields := strings.Fields(firstLine(join(h.ProcRoot, "loadavg")))
	if len(fields) >= 3 {
		s.Load1 = parseFloat(fields[0])
		s.Load5 = parseFloat(fields[1])
		s.Load15 = parseFloat(fields[2])
	}
}

func (h *Host) fillUptime(s *HostSample) {
	fields := strings.Fields(firstLine(join(h.ProcRoot, "uptime")))
	if len(fields) >= 1 {
		s.UptimeSeconds = parseFloat(fields[0])
	}
}

func (h *Host) readDiskstats(snap *snapshot) {
	_ = eachLine(join(h.ProcRoot, "diskstats"), func(line string) error {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil
		}
		name := fields[2]
		if !h.isPhysicalDisk(name) {
			return nil
		}
		snap.disk.readsCompleted += parseUint(fields[3])
		snap.disk.sectorsRead += parseUint(fields[5])
		snap.disk.writesCompleted += parseUint(fields[7])
		snap.disk.sectorsWritten += parseUint(fields[9])
		return nil
	})
}

// isPhysicalDisk keeps whole block devices and drops partitions, loop devices,
// and devices stacked on another disk (device-mapper, md) so totals are not
// counted twice. Per-device series are not stored.
func (h *Host) isPhysicalDisk(name string) bool {
	for _, prefix := range []string{"loop", "ram", "zram", "fd", "sr"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	block := join(h.SysRoot, "block", name)
	st, err := os.Stat(block)
	if err != nil || !st.IsDir() {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(block, "slaves"))
	if err == nil && len(entries) > 0 {
		return false
	}
	return true
}

func (h *Host) readNetDev(snap *snapshot) {
	_ = eachLine(join(h.ProcRoot, "net", "dev"), func(line string) error {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			return nil
		}
		iface := strings.TrimSpace(name)
		if !includeInterface(iface) {
			return nil
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			return nil
		}
		snap.net.rxBytes += parseUint(fields[0])
		snap.net.txBytes += parseUint(fields[8])
		return nil
	})
}

// includeInterface skips loopback and virtual interfaces that would double-count
// traffic already seen on a physical NIC. Per-interface series are not stored.
func includeInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, prefix := range []string{"veth", "docker", "br-", "virbr", "tap", "tun", "cni", "flannel", "kube-", "lxcbr", "dummy"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// BootTime reads btime from /proc/stat.
func (h *Host) BootTime() int64 {
	var bt int64
	_ = eachLine(join(h.ProcRoot, "stat"), func(line string) error {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			bt = int64(parseUint(strings.TrimSpace(v)))
		}
		return nil
	})
	return bt
}

// BootID reads /proc/sys/kernel/random/boot_id.
func (h *Host) BootID() string {
	return firstLine(join(h.ProcRoot, "sys", "kernel", "random", "boot_id"))
}

// Mount is one /proc/mounts entry.
type Mount struct {
	Device string
	Point  string
	FSType string
	Opts   string
}

var pseudoFilesystems = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "cgroup": true,
	"cgroup2": true, "pstore": true, "bpf": true, "tracefs": true, "debugfs": true,
	"securityfs": true, "hugetlbfs": true, "mqueue": true, "configfs": true,
	"fusectl": true, "autofs": true, "binfmt_misc": true, "efivarfs": true,
	"nsfs": true, "ramfs": true, "rpc_pipefs": true, "squashfs": true,
	"selinuxfs": true, "fuse.gvfsd-fuse": true, "overlay": true, "tmpfs": true,
}

// Mounts parses /proc/mounts.
func (h *Host) Mounts() []Mount {
	var out []Mount
	_ = eachLine(join(h.ProcRoot, "mounts"), func(line string) error {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return nil
		}
		out = append(out, Mount{
			Device: unescapeMount(fields[0]),
			Point:  unescapeMount(fields[1]),
			FSType: fields[2],
			Opts:   fields[3],
		})
		return nil
	})
	return out
}

func defaultMountFilter(m Mount) bool {
	if pseudoFilesystems[m.FSType] {
		return false
	}
	if !strings.HasPrefix(m.Device, "/") && !strings.Contains(m.Device, ":") {
		return false
	}
	return true
}

func unescapeMount(in string) string {
	if !strings.Contains(in, `\`) {
		return in
	}
	var b strings.Builder
	for i := 0; i < len(in); i++ {
		if in[i] == '\\' && i+3 < len(in) {
			if n, err := strconv.ParseUint(in[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(in[i])
	}
	return b.String()
}
