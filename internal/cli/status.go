package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

// StatusReport is the JSON shape printed by `wbt status --format json`.
// StartedAt is a pointer so json:"omitempty" actually drops the field
// when we don't know the start time (PID-file-only path).
type StatusReport struct {
	PID       int        `json:"pid"`
	Port      int        `json:"port"`
	Transport string     `json:"transport"`
	Healthy   bool       `json:"healthy"`
	PIDFile   string     `json:"pid_file"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	Version   string     `json:"version,omitempty"`
}

// RunStatus implements `wbt status`. It reads the PID file, checks
// liveness, probes /health on the configured port, and prints the result.
//
// Flags:
//
//	--format plain   (default) one-line human summary
//	--format json    JSON shape per StatusReport
//
// Exit code: 0 when the server is healthy; 1 when not running OR PID file
// references a dead process; 2 on probe failure (port unreachable but PID
// alive — caller may want to inspect logs).
func RunStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	format := fs.String("format", "plain", "output format: plain | json")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("status: %w", err)
	}
	report, exitErr := collectStatus()
	switch strings.ToLower(*format) {
	case "json":
		if err := emitJSON(os.Stdout, report); err != nil {
			return err
		}
	case "plain":
		emitPlain(os.Stdout, report)
	default:
		return fmt.Errorf("--format must be 'plain' or 'json'; got %q", *format)
	}
	return exitErr
}

// collectStatus gathers the StatusReport. The returned error reports the
// exit code semantics described in RunStatus.
func collectStatus() (StatusReport, error) {
	pidPath := lifecycle.PIDPath("")
	report := StatusReport{PIDFile: pidPath, Transport: string(MCPTransportHTTP)}
	pid, err := lifecycle.ReadPID(pidPath)
	if errors.Is(err, lifecycle.ErrNoPIDFile) {
		return report, errStatusNotRunning
	}
	if err != nil {
		return report, fmt.Errorf("reading pid file: %w", err)
	}
	report.PID = pid

	sup := lifecycle.NewNohupSupervisor()
	st, err := sup.Status(pid)
	if err != nil {
		return report, fmt.Errorf("checking pid %d: %w", pid, err)
	}
	if !st.Running {
		return report, errStatusNotRunning
	}

	port := readPortFromEnvOrConfig()
	report.Port = port
	report.Healthy = probeHealth(port)
	if !report.Healthy {
		return report, errStatusUnhealthy
	}
	return report, nil
}

// readPortFromEnvOrConfig picks PORT > config.Port > 8420 for status output.
// (No CLI flag override on status — what matters is the running server's
// port, which is opaque to us; we report the one we'd try if the user
// re-ran setup.)
func readPortFromEnvOrConfig() int {
	if v := strings.TrimSpace(os.Getenv("PORT")); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p <= 65535 {
			return p
		}
	}
	cfgPath, err := ConfigPath()
	if err == nil {
		if cfg, err := loadConfigBytes(cfgPath); err == nil && cfg.Port != "" {
			if p, err := strconv.Atoi(cfg.Port); err == nil && p > 0 && p <= 65535 {
				return p
			}
		}
	}
	return 8420
}

// emitJSON marshals report as indented JSON.
func emitJSON(w *os.File, report StatusReport) error {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling status: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("writing status: %w", err)
	}
	return nil
}

// emitPlain prints a human-readable one-liner.
func emitPlain(w *os.File, report StatusReport) {
	switch {
	case report.PID == 0:
		fmt.Fprintln(w, "wayneblacktea: not running (no PID file)")
	case !report.Healthy && report.Port == 0:
		fmt.Fprintf(w, "wayneblacktea: PID %d alive but unreachable (no port info)\n", report.PID)
	case report.Healthy:
		fmt.Fprintf(w, "wayneblacktea: running (pid %d, port %d, transport %s)\n",
			report.PID, report.Port, report.Transport)
	default:
		fmt.Fprintf(w, "wayneblacktea: PID %d alive but /health on port %d unhealthy\n",
			report.PID, report.Port)
	}
}

// Sentinel errors so callers (RunStatus) can map to exit codes without
// pattern-matching strings.
var (
	errStatusNotRunning = errors.New("wayneblacktea is not running")
	errStatusUnhealthy  = errors.New("wayneblacktea PID is alive but /health failed")
)
