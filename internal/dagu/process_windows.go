//go:build windows

package dagu

import "os/exec"

func prepareProcess(*exec.Cmd)        {}
func killProcess(cmd *exec.Cmd) error { return cmd.Process.Kill() }
