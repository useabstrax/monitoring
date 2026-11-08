// Package ipc is the root-only Unix socket protocol between the monitoring
// CLI and the monitoring daemon. Frames are a 4-byte big-endian length followed
// by a JSON body. It is local IPC, not a remote API.
package ipc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	maxRequest  = 64 * 1024
	maxResponse = 1024 * 1024
)

// Request is one CLI call.
type Request struct {
	Op         string `json:"op"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	MaxPoints  int    `json:"max_points,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
	At         string `json:"at,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Name       string `json:"name,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

// Response is one non-streaming reply.
type Response struct {
	OK        bool            `json:"ok"`
	ErrorCode string          `json:"error_code,omitempty"`
	Message   string          `json:"message,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxResponse {
		return fmt.Errorf("frame exceeds %d bytes", maxResponse)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader, limit int) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || int(n) > limit {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Listen opens the socket, replacing a stale socket file, and restricts it
// to mode 0600.
func Listen(path string) (*net.UnixListener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// Dial connects to the daemon socket.
func Dial(path string) (*net.UnixConn, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("unexpected connection type")
	}
	return uc, nil
}
