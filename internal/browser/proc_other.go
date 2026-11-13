//go:build !linux

package browser

import (
	"os"
	"os/exec"
	"syscall"
)

func procAttr() *syscall.SysProcAttr { return nil }
func terminate(cmd *exec.Cmd)        { _ = cmd.Process.Signal(os.Interrupt) }
func kill(cmd *exec.Cmd)             { _ = cmd.Process.Kill() }
