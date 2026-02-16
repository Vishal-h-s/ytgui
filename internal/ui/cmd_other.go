//go:build !windows

package ui

import "os/exec"

func setCmdHideWindow(cmd *exec.Cmd) {}
