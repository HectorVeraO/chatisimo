//go:build e2e && windows

package e2e

import (
	"fmt"
	"os"
	"syscall"
)

// processAttributes puts the child in its own process group, which is what lets a console
// control event target it alone.
func processAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// sendStop sends CTRL_BREAK_EVENT to the child's process group. Go delivers it as
// os.Interrupt, which the run command already handles. The call needs the child to share this
// process's console; without one it fails, and that is a harness error.
func sendStop(process *os.Process) error {
	if process == nil {
		return errNoProcess
	}

	kernel32, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		return fmt.Errorf("load kernel32: %w", err)
	}

	generate, err := kernel32.FindProc("GenerateConsoleCtrlEvent")
	if err != nil {
		return fmt.Errorf("find GenerateConsoleCtrlEvent: %w", err)
	}

	//nolint:gosec // a process ID is a small positive number
	result, _, callErr := generate.Call(uintptr(syscall.CTRL_BREAK_EVENT), uintptr(process.Pid))
	if result == 0 {
		return fmt.Errorf("GenerateConsoleCtrlEvent for pid %d: %w", process.Pid, callErr)
	}

	return nil
}
