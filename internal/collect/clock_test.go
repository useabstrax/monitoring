package collect

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

func TestClockTicksFromAuxv(t *testing.T) {
	width := int(unsafe.Sizeof(uintptr(0)))
	buf := make([]byte, width*4)
	putWord(buf[0:width], atCLKTCK)
	putWord(buf[width:2*width], 100)
	if got := clockTicksFromAuxv(buf); got != 100 {
		t.Fatalf("ticks = %d", got)
	}
	if got := clockTicksFromAuxv(nil); got != 0 {
		t.Fatalf("empty auxv = %d", got)
	}
	if got := ClockTicks(); got == 0 {
		t.Fatal("ClockTicks returned 0")
	}
}

func putWord(b []byte, v uint64) {
	if len(b) == 8 {
		binary.NativeEndian.PutUint64(b, v)
		return
	}
	binary.NativeEndian.PutUint32(b, uint32(v))
}
