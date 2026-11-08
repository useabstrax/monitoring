package collect

import (
	"encoding/binary"
	"os"
	"unsafe"
)

// AT_CLKTCK is the auxiliary-vector entry for the kernel clock-tick rate.
// /proc stat counters are in these ticks. glibc exposes the same value as
// sysconf(_SC_CLK_TCK). golang.org/x/sys does not provide Sysconf on Linux.
const atCLKTCK = 17

// ClockTicks returns the kernel USER_HZ value used by /proc CPU counters.
// It is 100 on mainstream Linux. Tests pass an explicit value instead.
func ClockTicks() uint64 {
	data, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 100
	}
	if v := clockTicksFromAuxv(data); v > 0 {
		return v
	}
	return 100
}

func clockTicksFromAuxv(data []byte) uint64 {
	width := int(unsafe.Sizeof(uintptr(0)))
	if width != 4 && width != 8 {
		return 0
	}
	for i := 0; i+2*width <= len(data); i += 2 * width {
		typ := nativeWord(data[i : i+width])
		val := nativeWord(data[i+width : i+2*width])
		if typ == 0 {
			return 0
		}
		if typ == atCLKTCK && val > 0 {
			return val
		}
	}
	return 0
}

func nativeWord(b []byte) uint64 {
	if len(b) == 8 {
		return binary.NativeEndian.Uint64(b)
	}
	return uint64(binary.NativeEndian.Uint32(b))
}
