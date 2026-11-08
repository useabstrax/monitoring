//go:build !linux

package collect

// ClockTicks returns the conventional Linux USER_HZ. Non-Linux hosts do not
// publish /proc, so collection tests inject the tick rate explicitly.
func ClockTicks() uint64 { return 100 }
