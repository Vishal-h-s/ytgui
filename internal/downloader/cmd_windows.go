//go:build windows

package downloader

import (
	"os/exec"
	"syscall"
)

func setCmdHideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
