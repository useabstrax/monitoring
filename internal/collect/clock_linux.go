//go:build linux

package collect

import "golang.org/x/sys/unix"

// ClockTicks returns the kernel USER_HZ value used by /proc CPU counters.
func ClockTicks() uint64 {
	v, err := unix.Sysconf(unix.SC_CLK_TCK)
	if err != nil || v <= 0 {
		return 100
	}
	return uint64(v)
}
