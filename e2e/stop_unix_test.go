//go:build e2e && !windows

package e2e

import (
	"fmt"
	"os"
	"syscall"
)

// processAttributes needs nothing on Unix: SIGTERM reaches the child by its PID.
func processAttributes() *syscall.SysProcAttr {
	return nil
}

// sendStop asks the process to shut down the way a service manager does.
func sendStop(process *os.Process) error {
	if process == nil {
		return errNoProcess
	}

	err := process.Signal(syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	return nil
}
