//go:build !windows

package library

import (
	"os/exec"
	"syscall"
)

// Cancel the transport helpers too, so they cannot keep downloading after quit.
func cancelSourceProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
