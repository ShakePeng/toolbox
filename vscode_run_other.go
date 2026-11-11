//go:build !windows

package main

import "os/exec"

func vscodeCommandLine(command, folder string) *exec.Cmd {
	return exec.Command(command, folder)
}
