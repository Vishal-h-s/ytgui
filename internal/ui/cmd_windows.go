//go:build windows

package ui

import (
	"os/exec"
	"syscall"
)

func setCmdHideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
