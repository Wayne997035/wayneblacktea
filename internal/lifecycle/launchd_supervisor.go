package lifecycle

import (
	"context"
	"errors"
)

// LaunchdSupervisor is a placeholder for the Phase 3 macOS LaunchAgent
// integration. Each method currently returns errors.ErrUnsupported so the
// CLI can degrade gracefully if a caller wires it up too early. The real
// implementation will:
//
//  1. Write a per-user LaunchAgent plist to ~/Library/LaunchAgents/
//  2. Call `launchctl bootstrap gui/<uid> <plist>` to register it
//  3. Use `launchctl bootout` to unregister on Stop
//
// Keeping the type compile-time present (rather than #ifdef-ing it out)
// lets cmd/wbt code reference it with no build-tag complexity.
type LaunchdSupervisor struct{}

// NewLaunchdSupervisor returns a LaunchdSupervisor stub.
func NewLaunchdSupervisor() *LaunchdSupervisor {
	return &LaunchdSupervisor{}
}

// Start is not yet implemented; returns errors.ErrUnsupported.
func (s *LaunchdSupervisor) Start(_ context.Context, _ StartOptions) (*Handle, error) {
	return nil, errors.ErrUnsupported
}

// Stop is not yet implemented; returns errors.ErrUnsupported.
func (s *LaunchdSupervisor) Stop(_ context.Context, _ int) error {
	return errors.ErrUnsupported
}

// Status is not yet implemented; returns errors.ErrUnsupported.
func (s *LaunchdSupervisor) Status(_ int) (Status, error) {
	return Status{}, errors.ErrUnsupported
}
