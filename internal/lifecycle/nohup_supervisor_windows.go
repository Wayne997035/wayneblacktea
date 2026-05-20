//go:build windows

package lifecycle

import (
	"context"
	"errors"
)

// NohupSupervisor on Windows is a stub. The nohup model (fork + setsid +
// detached child via SIGHUP-ignored session leader) does not translate to
// Windows. A real Windows implementation would use the job-object API or a
// Windows Service. Until that lands, every method returns
// errors.ErrUnsupported.
type NohupSupervisor struct {
	TermGracePeriod int
}

// NewNohupSupervisor returns a NohupSupervisor stub.
func NewNohupSupervisor() *NohupSupervisor {
	return &NohupSupervisor{}
}

// Start is not implemented on Windows.
func (s *NohupSupervisor) Start(_ context.Context, _ StartOptions) (*Handle, error) {
	return nil, errors.ErrUnsupported
}

// Stop is not implemented on Windows.
func (s *NohupSupervisor) Stop(_ context.Context, _ int) error {
	return errors.ErrUnsupported
}

// Status is not implemented on Windows.
func (s *NohupSupervisor) Status(_ int) (Status, error) {
	return Status{}, errors.ErrUnsupported
}
