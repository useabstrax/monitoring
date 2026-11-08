package collect

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Identity is the Linux process identity that survives PID reuse.
type Identity struct {
	BootID     string
	PID        int
	StartTicks uint64
}

// ID is the public instance id.
func (i Identity) ID() string { return InstanceID(i.BootID, i.PID, i.StartTicks) }

// Instance is metadata that does not change over the life of the process.
type Instance struct {
	Identity
	PPID         int
	Name         string
	Exe          string
	Cmd          string
	CmdTruncated bool
	User         string
	UID          int
	StartUnix    int64
	Kernel       bool
}

// Metrics are the values that change between samples.
type Metrics struct {
	CPUPercent float64
	RSS        uint64
	MemPercent float64
	ReadBytes  uint64
	WriteBytes uint64
	ReadBps    float64
	WriteBps   float64
	Threads    int
	State      string
	Elapsed    time.Duration
}

// Proc is one sampled process.
type Proc struct {
	Instance
	Metrics
}

// ProcessSnapshot is one pass over /proc.
type ProcessSnapshot struct {
	Timestamp time.Time
	BootID    string
	// Procs are processes with a rate since the previous sample.
	Procs []Proc
	// First are processes seen for the first time. They have an identity and
	// no rate yet, so a process that exits before the next sample is still
	// attributable.
	First   []Instance
	Exited  []Identity
	Scanned int
}

type counters struct {
	cpuTicks   uint64
	readBytes  uint64
	writeBytes uint64
	when       time.Time
	ioOK       bool
	inst       Instance
	haveInst   bool
}

// Process samples processes. It is not safe for concurrent Collect calls.
type Process struct {
	ProcRoot    string
	ClockTicks  uint64
	PageSize    uint64
	MemTotal    uint64
	MaxCmdBytes int

	bootID string
	prev   map[Identity]counters
	users  *userCache
}

// NewProcess returns a process collector. clockTicks 0 uses ClockTicks().
func NewProcess(procRoot string, clockTicks uint64) *Process {
	if clockTicks == 0 {
		clockTicks = ClockTicks()
	}
	return &Process{
		ProcRoot:    procRoot,
		ClockTicks:  clockTicks,
		PageSize:    uint64(os.Getpagesize()),
		MaxCmdBytes: 4096,
		prev:        map[Identity]counters{},
		users:       newUserCache(),
	}
}

// SetMemTotal sets the host memory total used for memory percentages.
func (p *Process) SetMemTotal(n uint64) { p.MemTotal = n }

// SetMaxCommandLine sets the stored command-line limit.
func (p *Process) SetMaxCommandLine(n int) {
	if n > 0 {
		p.MaxCmdBytes = n
	}
}

// SetBootTime is retained for callers that already know btime. Start times are
// resolved from /proc/stat when Collect runs.
func (p *Process) Collect(now time.Time) (*ProcessSnapshot, error) {
	entries, err := os.ReadDir(p.ProcRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.ProcRoot, err)
	}
	bootID := firstLine(join(p.ProcRoot, "sys", "kernel", "random", "boot_id"))
	if bootID == "" {
		bootID = "unknown"
	}
	bootTime := p.bootTime()
	snap := &ProcessSnapshot{Timestamp: now, BootID: bootID}
	if p.bootID != "" && bootID != p.bootID {
		for id := range p.prev {
			snap.Exited = append(snap.Exited, id)
		}
		p.prev = map[Identity]counters{}
	}
	p.bootID = bootID

	seen := make(map[Identity]bool, len(p.prev))
	next := make(map[Identity]counters, len(p.prev)+8)

	for _, e := range entries {
		pid, ok := pidOf(e.Name())
		if !ok {
			continue
		}
		snap.Scanned++
		st, err := p.readStat(pid)
		if err != nil {
			// The process exited between readdir and the read. That is normal.
			continue
		}
		id := Identity{BootID: bootID, PID: pid, StartTicks: st.startTicks}
		seen[id] = true
		readBytes, writeBytes, ioOK := p.readIO(pid)
		cur := counters{
			cpuTicks:   st.utime + st.stime,
			readBytes:  readBytes,
			writeBytes: writeBytes,
			when:       now,
			ioOK:       ioOK,
		}
		prev, hadPrev := p.prev[id]
		if !hadPrev {
			cur.inst = p.readInstance(pid, st, id, bootTime)
			cur.haveInst = true
			snap.First = append(snap.First, cur.inst)
			next[id] = cur
			continue
		}
		elapsed := now.Sub(prev.when)
		if elapsed <= 0 {
			next[id] = prev
			continue
		}
		m := Metrics{
			Threads: st.threads,
			State:   st.state,
			RSS:     st.rssPages * p.PageSize,
			Elapsed: elapsed,
		}
		if cur.cpuTicks >= prev.cpuTicks && p.ClockTicks > 0 {
			cpuSeconds := float64(cur.cpuTicks-prev.cpuTicks) / float64(p.ClockTicks)
			m.CPUPercent = cpuSeconds / elapsed.Seconds() * 100
		}
		if ioOK && prev.ioOK {
			if cur.readBytes >= prev.readBytes {
				m.ReadBytes = cur.readBytes - prev.readBytes
				m.ReadBps = float64(m.ReadBytes) / elapsed.Seconds()
			}
			if cur.writeBytes >= prev.writeBytes {
				m.WriteBytes = cur.writeBytes - prev.writeBytes
				m.WriteBps = float64(m.WriteBytes) / elapsed.Seconds()
			}
		}
		if p.MemTotal > 0 {
			m.MemPercent = float64(m.RSS) / float64(p.MemTotal) * 100
		}
		inst := prev.inst
		if !prev.haveInst {
			inst = p.readInstance(pid, st, id, bootTime)
		} else {
			inst.PPID = st.ppid
		}
		cur.inst = inst
		cur.haveInst = true
		snap.Procs = append(snap.Procs, Proc{Instance: inst, Metrics: m})
		next[id] = cur
	}
	for id := range p.prev {
		if !seen[id] {
			snap.Exited = append(snap.Exited, id)
		}
	}
	p.prev = next
	return snap, nil
}

const pfKThread = 0x00200000

type statFields struct {
	comm         string
	state        string
	ppid         int
	utime        uint64
	stime        uint64
	threads      int
	startTicks   uint64
	rssPages     uint64
	kernelThread bool
}

func (p *Process) readStat(pid int) (statFields, error) {
	data, err := os.ReadFile(filepath.Join(p.ProcRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return statFields{}, err
	}
	return parseStat(data)
}

func parseStat(data []byte) (statFields, error) {
	var f statFields
	open := bytes.IndexByte(data, '(')
	close := bytes.LastIndexByte(data, ')')
	if open < 0 || close < 0 || close < open {
		return f, fmt.Errorf("malformed stat line")
	}
	f.comm = string(data[open+1 : close])
	rest := strings.Fields(string(data[close+1:]))
	const (
		fState     = 0
		fPPID      = 1
		fFlags     = 6
		fUTime     = 11
		fSTime     = 12
		fThreads   = 17
		fStartTime = 19
		fRSS       = 21
	)
	if len(rest) <= fRSS {
		return f, fmt.Errorf("stat line has %d trailing fields, need %d", len(rest), fRSS+1)
	}
	f.state = rest[fState]
	f.ppid, _ = strconv.Atoi(rest[fPPID])
	if flags, err := strconv.ParseUint(rest[fFlags], 10, 64); err == nil {
		f.kernelThread = flags&pfKThread != 0
	}
	f.utime, _ = strconv.ParseUint(rest[fUTime], 10, 64)
	f.stime, _ = strconv.ParseUint(rest[fSTime], 10, 64)
	f.threads, _ = strconv.Atoi(rest[fThreads])
	f.startTicks, _ = strconv.ParseUint(rest[fStartTime], 10, 64)
	f.rssPages, _ = strconv.ParseUint(rest[fRSS], 10, 64)
	return f, nil
}

func (p *Process) readIO(pid int) (read, write uint64, ok bool) {
	data, err := os.ReadFile(filepath.Join(p.ProcRoot, strconv.Itoa(pid), "io"))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch key {
		case "read_bytes":
			read, _ = strconv.ParseUint(strings.TrimSpace(val), 10, 64)
			ok = true
		case "write_bytes":
			write, _ = strconv.ParseUint(strings.TrimSpace(val), 10, 64)
			ok = true
		}
	}
	return read, write, ok
}

func (p *Process) readInstance(pid int, st statFields, id Identity, bootTime int64) Instance {
	dir := filepath.Join(p.ProcRoot, strconv.Itoa(pid))
	inst := Instance{Identity: id, PPID: st.ppid, Name: st.comm, UID: -1, Kernel: st.kernelThread}
	if raw, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		inst.Cmd = decodeCmdline(raw)
	}
	if inst.Cmd == "" {
		inst.Kernel = true
		inst.Cmd = "[" + st.comm + "]"
	} else if better := displayName(st.comm, inst.Cmd); better != "" {
		inst.Name = better
	}
	inst.Cmd, inst.CmdTruncated = TruncateCommand(inst.Cmd, p.MaxCmdBytes)
	if target, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		inst.Exe = target
	}
	if uid, ok := readUID(dir); ok {
		inst.UID = uid
		inst.User = p.users.name(uid)
	}
	if bootTime > 0 && p.ClockTicks > 0 {
		inst.StartUnix = bootTime + int64(st.startTicks/p.ClockTicks)
	}
	return inst
}

func (p *Process) bootTime() int64 {
	var bt int64
	_ = eachLine(join(p.ProcRoot, "stat"), func(line string) error {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			bt = int64(parseUint(strings.TrimSpace(v)))
		}
		return nil
	})
	return bt
}

func decodeCmdline(raw []byte) string {
	raw = bytes.TrimRight(raw, "\x00")
	if len(raw) == 0 {
		return ""
	}
	parts := bytes.Split(raw, []byte{0})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		out = append(out, string(part))
	}
	return strings.Join(out, " ")
}

var interpreters = map[string]bool{
	"dotnet": true, "java": true, "python": true, "python2": true, "python3": true,
	"node": true, "ruby": true, "perl": true, "php": true, "php-fpm": true,
	"mono": true, "sh": true, "bash": true, "uwsgi": true, "gunicorn": true,
}

func displayName(comm, cmd string) string {
	base := comm
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if !interpreters[base] {
		return ""
	}
	fields := strings.Fields(cmd)
	start := 0
	if len(fields) > 0 {
		start = 1
	}
	for _, f := range fields[start:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		name := f
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if name == "" || name == base {
			continue
		}
		return name
	}
	return ""
}

func readUID(dir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "status"))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					n, err := strconv.Atoi(fields[0])
					if err == nil {
						return n, true
					}
				}
			}
		}
	}
	return 0, false
}

func pidOf(name string) (int, bool) {
	if name == "" || name[0] < '1' || name[0] > '9' {
		return 0, false
	}
	n, err := strconv.Atoi(name)
	if err != nil {
		return 0, false
	}
	return n, true
}

type userCache struct {
	mu    sync.Mutex
	names map[int]string
}

func newUserCache() *userCache { return &userCache{names: map[int]string{}} }

func (c *userCache) name(uid int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.names[uid]; ok {
		return n
	}
	name := strconv.Itoa(uid)
	if u, err := user.LookupId(name); err == nil && u.Username != "" {
		name = u.Username
	}
	c.names[uid] = name
	return name
}
