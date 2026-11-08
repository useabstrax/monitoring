package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
)

// Client calls the local daemon.
type Client struct {
	path string
}

// NewClient returns a client for the socket at path.
func NewClient(path string) *Client { return &Client{path: path} }

// Call performs one request.
func (c *Client) Call(ctx context.Context, req Request) (json.RawMessage, error) {
	conn, err := Dial(c.path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || isPermission(err) {
			return nil, moniterr.E("permission_denied", "monitoring is root-only; retry with sudo")
		}
		return nil, moniterr.E("not_running", "monitoring daemon is not running (try: sudo abstrax monitoring start)")
	}
	defer conn.Close()
	deadline := time.Now().Add(30 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(conn, body); err != nil {
		return nil, err
	}
	frame, err := readFrame(conn, maxResponse)
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(frame, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		code := resp.ErrorCode
		if code == "" {
			code = "internal"
		}
		return nil, moniterr.E(code, resp.Message)
	}
	return resp.Data, nil
}

// Live reads sample frames until ctx is cancelled or the daemon closes.
func (c *Client) Live(ctx context.Context, fn func([]byte) error) error {
	conn, err := Dial(c.path)
	if err != nil {
		if isPermission(err) {
			return moniterr.E("permission_denied", "monitoring is root-only; retry with sudo")
		}
		return moniterr.E("not_running", "monitoring daemon is not running (try: sudo abstrax monitoring start)")
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	body, _ := json.Marshal(Request{Op: "live"})
	if err := writeFrame(conn, body); err != nil {
		return err
	}
	for {
		_ = conn.SetReadDeadline(time.Time{})
		frame, err := readFrame(conn, maxResponse)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		var peek struct {
			Type string `json:"type"`
			OK   *bool  `json:"ok"`
		}
		_ = json.Unmarshal(frame, &peek)
		if peek.OK != nil && !*peek.OK {
			var resp Response
			_ = json.Unmarshal(frame, &resp)
			return moniterr.E(resp.ErrorCode, resp.Message)
		}
		if err := fn(frame); err != nil {
			return err
		}
	}
}

func isPermission(err error) bool {
	return err != nil && (os.IsPermission(err) || strings.Contains(err.Error(), "permission denied"))
}
