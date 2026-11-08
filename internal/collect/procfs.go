// Package collect reads Linux host and process metrics from /proc and /sys.
//
// The arithmetic is adapted from Detective Lumberjack's collector and process
// packages. There is no hub, spool, or remote protocol here: samples are handed
// to the monitoring daemon, which owns storage.
package collect

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func eachLine(path string, fn func(line string) error) error {
	data, err := readFile(path)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if err := fn(sc.Text()); err != nil {
			return err
		}
	}
	return sc.Err()
}

func parseUint(s string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n
}

func parseFloat(s string) float64 {
	n, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return n
}

func join(root string, parts ...string) string {
	return filepath.Join(append([]string{root}, parts...)...)
}

func firstLine(path string) string {
	data, err := readFile(path)
	if err != nil {
		return ""
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		data = data[:i]
	}
	return strings.TrimSpace(string(data))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func isPID(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return true
}
