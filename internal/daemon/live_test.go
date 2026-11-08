package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/collect"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/config"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/ipc"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/layout"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/testproc"
)

func TestLivePersistsAndFanout(t *testing.T) {
	// Unix socket paths must fit in sockaddr_un (104 bytes on Darwin).
	dir, err := os.MkdirTemp("/tmp", "mon")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	tree := testproc.New(t)
	cfg := config.Default()
	cfg.Sampling.HostInterval = config.Duration(time.Second)
	cfg.Sampling.ProcessInterval = config.Duration(time.Second)
	paths := layout.Paths{ConfigFile: filepath.Join(dir, "config.yaml"), StateDir: filepath.Join(dir, "state")}
	if err := config.Save(paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	d := New(cfg, paths, collect.NewHost(tree.Proc, tree.Sys), collect.NewProcess(tree.Proc, 100), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	client := ipc.NewClient(paths.Socket())
	liveCtx, stop := context.WithTimeout(context.Background(), 12*time.Second)
	defer stop()

	var mu sync.Mutex
	var samples []json.RawMessage
	errCh := make(chan error, 1)
	go func() {
		var err error
		for liveCtx.Err() == nil {
			err = client.Live(liveCtx, func(frame []byte) error {
				mu.Lock()
				samples = append(samples, append(json.RawMessage(nil), frame...))
				mu.Unlock()
				return nil
			})
			if liveCtx.Err() != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		errCh <- err
	}()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(samples)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	n := len(samples)
	mu.Unlock()
	if n < 2 {
		select {
		case err := <-done:
			t.Fatalf("got %d live samples; daemon exited: %v", n, err)
		default:
			t.Fatalf("got %d live samples; socket %s", n, paths.Socket())
		}
	}
	st, err := client.Call(context.Background(), ipc.Request{Op: "status"})
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Collecting bool `json:"collecting"`
	}
	if err := json.Unmarshal(st, &status); err != nil || !status.Collecting {
		t.Fatalf("status = %s err %v", st, err)
	}
	info, err := os.Stat(paths.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("latest.json mode = %o", info.Mode().Perm())
	}
	var snap struct {
		SchemaVersion int `json:"schema_version"`
		Vitals        struct {
			CPUCores int `json:"cpu_cores"`
		} `json:"vitals"`
	}
	raw, err := os.ReadFile(paths.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &snap); err != nil || snap.SchemaVersion != 1 || snap.Vitals.CPUCores != 4 {
		t.Fatalf("snapshot = %+v %s", snap, raw)
	}

	// A subscriber that stops reading must not stop the others.
	blockCtx, blockCancel := context.WithCancel(context.Background())
	defer blockCancel()
	blocked := make(chan struct{})
	var once sync.Once
	go func() {
		_ = client.Live(blockCtx, func([]byte) error {
			once.Do(func() { close(blocked) })
			<-blockCtx.Done()
			return blockCtx.Err()
		})
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("second subscriber received nothing")
	}
	before := n
	time.Sleep(1500 * time.Millisecond)
	mu.Lock()
	after := len(samples)
	mu.Unlock()
	if after <= before {
		t.Fatal("a blocked subscriber stalled the live stream")
	}
	stop()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
	_ = errCh
}
