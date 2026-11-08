package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
)

const (
	maxConcurrent  = 4
	maxSubscribers = 8
)

// Handler is implemented by the daemon.
type Handler interface {
	Handle(ctx context.Context, req Request) (any, error)
	Subscribe(ctx context.Context) (<-chan []byte, func(), error)
}

// Serve accepts connections until ctx is cancelled or ln is closed.
func Serve(ctx context.Context, ln *net.UnixListener, h Handler) {
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			if err := authorize(conn); err != nil {
				writeErr(conn, err)
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			frame, err := readFrame(conn, maxRequest)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					writeErr(conn, moniterr.E("invalid_argument", "could not read request"))
				}
				return
			}
			_ = conn.SetReadDeadline(time.Time{})
			var req Request
			if err := json.Unmarshal(frame, &req); err != nil {
				writeErr(conn, moniterr.E("invalid_argument", "request is not JSON"))
				return
			}
			if req.Op == "live" {
				serveLive(ctx, conn, h)
				return
			}
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			default:
				writeErr(conn, moniterr.E("busy", "too many monitoring queries"))
				return
			}
			data, err := h.Handle(ctx, req)
			if err != nil {
				writeErr(conn, err)
				return
			}
			raw, err := json.Marshal(data)
			if err != nil {
				writeErr(conn, err)
				return
			}
			body, _ := json.Marshal(Response{OK: true, Data: raw})
			if len(body) > maxResponse {
				writeErr(conn, moniterr.E("invalid_argument", "response exceeds the size limit"))
				return
			}
			_ = writeFrame(conn, body)
		}()
	}
}

func serveLive(ctx context.Context, conn *net.UnixConn, h Handler) {
	ch, cancel, err := h.Subscribe(ctx)
	if err != nil {
		writeErr(conn, err)
		return
	}
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := writeFrame(conn, msg); err != nil {
				return
			}
		}
	}
}

func writeErr(conn net.Conn, err error) {
	body, _ := json.Marshal(Response{OK: false, ErrorCode: moniterr.Code(err), Message: err.Error()})
	_ = writeFrame(conn, body)
}
