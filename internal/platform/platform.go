// Package platform checks that this host is one Abstrax supports.
//
// The distro decision is not reimplemented here. Setup asks the Abstrax CLI
// (`abstrax doctor --json`) and honours its `supported` field, which is the
// current platform source of truth. Collectors themselves only require Linux
// /proc and /sys, so they behave the same way on every supported family.
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Result is the support decision from the Abstrax CLI.
type Result struct {
	Supported    bool
	Family       string
	SupportLevel string
	DistroID     string
	Note         string
}

type envelope struct {
	Status string `json:"status"`
	Data   struct {
		Supported   bool   `json:"supported"`
		SupportNote string `json:"support_note"`
		Profile     struct {
			Family       string `json:"family"`
			SupportLevel string `json:"support_level"`
			DistroID     string `json:"distro_id"`
		} `json:"profile"`
	} `json:"data"`
}

// Check reports whether monitoring should run on this machine.
func Check(ctx context.Context) (Result, error) {
	if runtime.GOOS != "linux" {
		return Result{}, fmt.Errorf("unsupported platform: Abstrax Monitoring requires Linux (this host is %s)", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return Result{}, fmt.Errorf("unsupported platform: architecture %s is not supported", runtime.GOARCH)
	}
	if _, err := os.Stat("/proc/stat"); err != nil {
		return Result{}, fmt.Errorf("unsupported platform: /proc/stat is not available: %w", err)
	}
	bin, err := FindAbstrax()
	if err != nil {
		return Result{}, err
	}
	return ConfirmDoctor(ctx, bin)
}

// FindAbstrax locates the Abstrax CLI.
func FindAbstrax() (string, error) {
	if p := os.Getenv("ABSTRAX_BINARY"); p != "" {
		return p, nil
	}
	p, err := exec.LookPath("abstrax")
	if err != nil {
		return "", fmt.Errorf("cannot validate platform support: abstrax CLI not found (set ABSTRAX_BINARY or install Abstrax)")
	}
	return p, nil
}

// ConfirmDoctor runs `abstrax doctor --json` and accepts only a supported host.
func ConfirmDoctor(ctx context.Context, bin string) (Result, error) {
	if bin == "" {
		return Result{}, fmt.Errorf("cannot validate platform support: abstrax CLI not found")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "doctor", "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return Result{}, fmt.Errorf("platform check failed: %s", msg)
	}
	res, err := ParseDoctor(stdout.Bytes())
	if err != nil {
		return Result{}, err
	}
	if !res.Supported {
		note := res.Note
		if note == "" {
			note = "this operating system is outside the Abstrax support matrix"
		}
		return res, fmt.Errorf("unsupported platform: %s", note)
	}
	return res, nil
}

// ParseDoctor reads an `abstrax doctor --json` payload.
func ParseDoctor(data []byte) (Result, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Result{}, fmt.Errorf("platform check returned malformed JSON: %w", err)
	}
	if env.Status != "" && env.Status != "success" {
		return Result{}, fmt.Errorf("platform check failed: doctor status %q", env.Status)
	}
	return Result{
		Supported:    env.Data.Supported,
		Family:       env.Data.Profile.Family,
		SupportLevel: env.Data.Profile.SupportLevel,
		DistroID:     env.Data.Profile.DistroID,
		Note:         env.Data.SupportNote,
	}, nil
}
