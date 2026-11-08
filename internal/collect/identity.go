package collect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// InstanceID is the stable public identity of one process instance.
// It is derived from boot_id, pid, and the /proc stat starttime field so a
// recycled PID or a reboot cannot merge two histories.
func InstanceID(bootID string, pid int, startTicks uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", bootID, pid, startTicks)))
	return hex.EncodeToString(sum[:16])
}

// TruncateCommand returns s limited to max bytes, without splitting a rune.
// truncated is true only when bytes were removed.
func TruncateCommand(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut], true
}
