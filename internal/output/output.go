// Package output renders command results in Abstrax CLI conventions:
// human text, a single --json object, or --json-stream NDJSON.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Result is the standard structured result matching Abstrax CLI conventions.
type Result struct {
	Status    string `json:"status"`
	Action    string `json:"action"`
	Summary   string `json:"summary,omitempty"`
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Data      any    `json:"data,omitempty"`
}

type progressEvent struct {
	Type    string `json:"type"`
	Action  string `json:"action"`
	Step    string `json:"step"`
	Message string `json:"message"`
}

type streamResult struct {
	Type string `json:"type"`
	Result
}

// Success builds a successful result.
func Success(action, summary string, data any) Result {
	return Result{Status: "success", Action: action, Summary: summary, Data: data}
}

// Failure builds a failure result.
func Failure(action, errorCode, message string) Result {
	return Result{Status: "error", Action: action, ErrorCode: errorCode, Message: message}
}

// Printer writes human, --json, or --json-stream output.
type Printer struct {
	Out        io.Writer
	Err        io.Writer
	JSONMode   bool
	JSONStream bool
	Quiet      bool
	VerboseOn  bool
	NoColor    bool
}

// NewPrinter creates a Printer writing to stdout/stderr.
func NewPrinter(jsonMode, jsonStream, quiet, verbose, noColor bool) *Printer {
	return &Printer{
		Out: os.Stdout, Err: os.Stderr,
		JSONMode: jsonMode, JSONStream: jsonStream,
		Quiet: quiet, VerboseOn: verbose, NoColor: noColor,
	}
}

// Machine reports whether output is machine-readable.
func (p *Printer) Machine() bool { return p.JSONMode || p.JSONStream }

// Progress emits an NDJSON progress line in stream mode, else a human step.
func (p *Printer) Progress(action, step, message string) {
	if p.JSONStream {
		writeNDJSON(p.Out, progressEvent{Type: "progress", Action: action, Step: step, Message: message})
		return
	}
	if p.Machine() || p.Quiet {
		return
	}
	fmt.Fprintf(p.Out, "==> %s\n", message)
}

// Info prints an informational message unless quiet/machine.
func (p *Printer) Info(format string, args ...any) {
	if p.Quiet || p.Machine() {
		return
	}
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Verbose prints only when --verbose and not machine mode.
func (p *Printer) Verbose(format string, args ...any) {
	if !p.VerboseOn || p.Machine() {
		return
	}
	fmt.Fprintf(p.Out, "[verbose] "+format+"\n", args...)
}

// Line prints a plain line unless machine mode.
func (p *Printer) Line(format string, args ...any) {
	if p.Machine() {
		return
	}
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Warn prints a warning to stderr.
func (p *Printer) Warn(format string, args ...any) {
	if p.Machine() {
		return
	}
	fmt.Fprintf(p.Err, "WARNING: "+format+"\n", args...)
}

// Print writes a final result.
func (p *Printer) Print(r Result) {
	switch {
	case p.JSONStream:
		writeNDJSON(p.Out, streamResult{Type: "result", Result: r})
	case p.JSONMode:
		enc := json.NewEncoder(p.Out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(r)
	case r.Status == "error":
		fmt.Fprintf(p.Err, "ERROR: %s\n", r.Message)
	case r.Summary != "" && !p.Quiet:
		fmt.Fprintln(p.Out, r.Summary)
	}
}

// WriteNDJSON writes one JSON line and flushes a file when possible.
func WriteNDJSON(w io.Writer, v any) {
	writeNDJSON(w, v)
}

func writeNDJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	if f, ok := w.(*os.File); ok {
		_ = f.Sync()
	}
}
