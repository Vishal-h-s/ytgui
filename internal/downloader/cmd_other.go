//go:build !windows

package downloader

import "os/exec"

func setCmdHideWindow(cmd *exec.Cmd) {}
