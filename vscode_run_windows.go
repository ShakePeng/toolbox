//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// vscodeCommandLine 在 Windows 上区分 exe 和 cmd/bat。cmd/bat 必须经过
// cmd.exe 才能执行；同时隐藏控制台窗口，避免点开项目时闪一下黑框。
func vscodeCommandLine(command, folder string) *exec.Cmd {
	ext := strings.ToLower(filepath.Ext(command))
	var cmd *exec.Cmd
	if ext == ".exe" || ext == ".com" {
		cmd = exec.Command(command, folder)
	} else {
		cmd = exec.Command("cmd.exe", "/c", command, folder)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
