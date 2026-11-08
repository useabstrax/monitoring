// Package testproc builds synthetic /proc and /sys trees so collector tests do
// not depend on the machine they run on.
package testproc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tree is a fixture /proc plus /sys.
type Tree struct {
	Root string
	Proc string
	Sys  string
	t    *testing.T
}

// New creates a fixture tree with plausible defaults.
func New(t *testing.T) *Tree {
	t.Helper()
	root := t.TempDir()
	tree := &Tree{Root: root, Proc: filepath.Join(root, "proc"), Sys: filepath.Join(root, "sys"), t: t}
	tree.mkdir(tree.Proc)
	tree.mkdir(filepath.Join(tree.Proc, "net"))
	tree.mkdir(filepath.Join(tree.Proc, "sys", "kernel", "random"))
	tree.mkdir(filepath.Join(tree.Sys, "block", "sda"))
	tree.SetStat(CPUStat{
		Total:        CPUTimes{User: 100000, Nice: 500, System: 40000, Idle: 900000, IOWait: 3000, IRQ: 200, SoftIRQ: 300},
		Cores:        []CPUTimes{{User: 25000, System: 10000, Idle: 225000, IOWait: 750}, {User: 25000, System: 10000, Idle: 225000, IOWait: 750}, {User: 25000, System: 10000, Idle: 225000, IOWait: 750}, {User: 25000, System: 10000, Idle: 225000, IOWait: 750}},
		BootTimeUnix: 1754000000,
	})
	tree.SetMemInfo(16*1024*1024, 8*1024*1024, 6*1024*1024, 512*1024, 256*1024)
	tree.Write("loadavg", "1.52 0.98 0.71 3/812 91234\n")
	tree.Write("uptime", "864000.42 3400000.11\n")
	tree.SetDiskStats(1000, 20000, 500, 8000)
	tree.SetNetDev(1_000_000, 2_000_000)
	tree.Write("mounts", "/dev/sda1 / ext4 rw,relatime 0 0\n"+
		"/dev/sdb1 /var xfs rw,relatime 0 0\n"+
		"proc /proc proc rw 0 0\n"+
		"tmpfs /run tmpfs rw 0 0\n"+
		"overlay /var/lib/docker/overlay2 overlay rw 0 0\n")
	tree.Write(filepath.Join("sys", "kernel", "random", "boot_id"), "fixture-boot\n")
	return tree
}

func (tr *Tree) mkdir(path string) {
	tr.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		tr.t.Fatalf("mkdir %s: %v", path, err)
	}
}

// Write writes a file inside the fixture /proc.
func (tr *Tree) Write(rel, content string) {
	tr.t.Helper()
	path := filepath.Join(tr.Proc, rel)
	tr.mkdir(filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", path, err)
	}
}

// CPUTimes is one /proc/stat cpu row, in jiffies.
type CPUTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// CPUStat is /proc/stat.
type CPUStat struct {
	Total        CPUTimes
	Cores        []CPUTimes
	BootTimeUnix int64
}

// SetStat writes /proc/stat.
func (tr *Tree) SetStat(s CPUStat) {
	tr.t.Helper()
	var b strings.Builder
	row := func(name string, t CPUTimes) {
		fmt.Fprintf(&b, "%s %d %d %d %d %d %d %d %d 0 0\n",
			name, t.User, t.Nice, t.System, t.Idle, t.IOWait, t.IRQ, t.SoftIRQ, t.Steal)
	}
	row("cpu ", s.Total)
	for i, c := range s.Cores {
		row(fmt.Sprintf("cpu%d", i), c)
	}
	fmt.Fprintf(&b, "btime %d\n", s.BootTimeUnix)
	tr.Write("stat", b.String())
}

// SetMemInfo writes /proc/meminfo. Sizes are kibibytes.
func (tr *Tree) SetMemInfo(totalKB, availableKB, cachedKB, swapTotalKB, swapFreeKB uint64) {
	tr.t.Helper()
	tr.Write("meminfo", fmt.Sprintf(
		"MemTotal:       %d kB\nMemFree:        %d kB\nMemAvailable:   %d kB\nBuffers:        %d kB\nCached:         %d kB\nSwapTotal:      %d kB\nSwapFree:       %d kB\nSReclaimable:   %d kB\n",
		totalKB, availableKB/2, availableKB, 1024, cachedKB, swapTotalKB, swapFreeKB, 1024))
}

// SetDiskStats writes /proc/diskstats. Sectors are 512-byte units.
func (tr *Tree) SetDiskStats(reads, sectorsRead, writes, sectorsWritten uint64) {
	tr.t.Helper()
	tr.Write("diskstats", fmt.Sprintf(
		"   8       0 sda %d 0 %d 0 %d 0 %d 0 0 0 0\n"+
			"   8       1 sda1 %d 0 %d 0 %d 0 %d 0 0 0 0\n"+
			"   7       0 loop0 9 0 99 0 9 0 99 0 0 0 0\n",
		reads, sectorsRead, writes, sectorsWritten,
		reads*10, sectorsRead*10, writes*10, sectorsWritten*10))
}

// SetNetDev writes /proc/net/dev with loopback and a docker bridge that must
// be excluded from the host totals.
func (tr *Tree) SetNetDev(rx, tx uint64) {
	tr.t.Helper()
	tr.Write(filepath.Join("net", "dev"), fmt.Sprintf(
		"Inter-|   Receive                                                |  Transmit\n"+
			" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"+
			"    lo: 5000 1 0 0 0 0 0 0 5000 1 0 0 0 0 0 0\n"+
			"  eth0: %d 1 0 0 0 0 0 0 %d 1 0 0 0 0 0 0\n"+
			"docker0: 9000 1 0 0 0 0 0 0 8000 1 0 0 0 0 0 0\n",
		rx, tx))
}

// SetBootID sets the fixture boot id.
func (tr *Tree) SetBootID(id string) {
	tr.t.Helper()
	tr.Write(filepath.Join("sys", "kernel", "random", "boot_id"), id+"\n")
}

// Proc describes a fixture process.
type Proc struct {
	PID        int
	PPID       int
	Comm       string
	State      string
	UTime      uint64
	STime      uint64
	Threads    int
	StartTicks uint64
	RSSPages   uint64
	Cmdline    string
	Exe        string
	ReadBytes  uint64
	WriteBytes uint64
	NoIO       bool
	Kernel     bool
	UID        int
}

const pfKThread = 0x00200000

// SetProc writes a process directory.
func (tr *Tree) SetProc(p Proc) {
	tr.t.Helper()
	dir := filepath.Join(tr.Proc, fmt.Sprint(p.PID))
	tr.mkdir(dir)
	if p.State == "" {
		p.State = "S"
	}
	if p.Threads == 0 {
		p.Threads = 1
	}
	var flags uint64
	if p.Kernel {
		flags = pfKThread
	}
	fields := []any{
		p.PID, "(" + p.Comm + ")", p.State, p.PPID,
		p.PID, p.PID, 0, -1, flags,
		0, 0, 0, 0,
		p.UTime, p.STime, 0, 0,
		20, 0, p.Threads, 0,
		p.StartTicks, 0, p.RSSPages,
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = fmt.Sprint(f)
	}
	tr.writeAt(filepath.Join(dir, "stat"), strings.Join(parts, " ")+"\n")
	cmdline := p.Cmdline
	if p.Kernel {
		cmdline = ""
	}
	tr.writeAt(filepath.Join(dir, "cmdline"), strings.ReplaceAll(cmdline, " ", "\x00")+"\x00")
	tr.writeAt(filepath.Join(dir, "status"), fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\n", p.Comm, p.UID, p.UID, p.UID, p.UID))
	if !p.NoIO {
		tr.writeAt(filepath.Join(dir, "io"), fmt.Sprintf("read_bytes: %d\nwrite_bytes: %d\n", p.ReadBytes, p.WriteBytes))
	} else {
		_ = os.Remove(filepath.Join(dir, "io"))
	}
	if p.Exe != "" {
		_ = os.Remove(filepath.Join(dir, "exe"))
		if err := os.Symlink(p.Exe, filepath.Join(dir, "exe")); err != nil {
			tr.t.Fatal(err)
		}
	}
}

// RemoveProc deletes a process directory.
func (tr *Tree) RemoveProc(pid int) {
	tr.t.Helper()
	if err := os.RemoveAll(filepath.Join(tr.Proc, fmt.Sprint(pid))); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr *Tree) writeAt(path, content string) {
	tr.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", path, err)
	}
}
