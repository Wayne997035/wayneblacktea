package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

// RunStop terminates the background wayneblacktea server identified by
// the PID file and removes the PID file on success. The operation is
// idempotent: missing PID file is reported as "already stopped" with
// exit code 0.
//
// args is reserved for future flags (--force, --timeout) and is currently
// validated to be empty.
func RunStop(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("stop: no positional arguments allowed; got %v", args)
	}
	pidPath := lifecycle.PIDPath("")
	pid, err := lifecycle.ReadPID(pidPath)
	if errors.Is(err, lifecycle.ErrNoPIDFile) {
		fmt.Fprintln(os.Stdout, "wbt: not running (no PID file)")
		return nil
	}
	if err != nil {
		// Corrupt PID file — remove it so the next setup is clean.
		_ = lifecycle.RemovePID(pidPath)
		return fmt.Errorf("stop: %w", err)
	}

	sup := lifecycle.NewNohupSupervisor()
	st, err := sup.Status(pid)
	if err != nil {
		return fmt.Errorf("stop: checking pid %d: %w", pid, err)
	}
	if !st.Running {
		fmt.Fprintf(os.Stdout, "wbt: pid %d already dead; removing stale PID file\n", pid)
		if err := lifecycle.RemovePID(pidPath); err != nil {
			return fmt.Errorf("removing stale PID file: %w", err)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sup.Stop(ctx, pid); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	if err := lifecycle.RemovePID(pidPath); err != nil {
		return fmt.Errorf("removing PID file: %w", err)
	}
	fmt.Fprintf(os.Stdout, "wbt: stopped wayneblacktea (pid %d)\n", pid)
	return nil
}

// RunRestart is stop + setup. We do not short-circuit on stop errors so
// that "stop failed because already stopped" still triggers a fresh
// setup attempt.
func RunRestart(args []string) error {
	if err := RunStop(nil); err != nil {
		// Surface but continue — `wbt restart` should still try to bring the server up.
		fmt.Fprintf(os.Stderr, "wbt: restart: stop returned %v (continuing to setup)\n", err)
	}
	return RunSetup(args)
}
