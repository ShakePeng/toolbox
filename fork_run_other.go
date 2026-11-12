//go:build !windows

package main

import "os/exec"

// forkCommandLine 用 `fork -C <仓库>` 打开指定仓库。
func forkCommandLine(command, folder string) *exec.Cmd {
	return exec.Command(command, "-C", folder)
}

// forkAppCommandLine 在非 Windows 平台没有应用本体回退，仅保留签名。
func forkAppCommandLine(appPath, folder string) *exec.Cmd {
	return exec.Command(appPath, folder)
}
