//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// forkCommandLine 在 Windows 上区分 exe 和 cmd/bat：cmd/bat 必须经过
// cmd.exe 才能执行，同时隐藏控制台窗口，避免打开项目时闪一下黑框。
// Windows 版 Fork 只支持 `Fork.exe <仓库路径>`，没有 macOS CLI 的 -C 参数。
func forkCommandLine(command, folder string) *exec.Cmd {
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

// forkAppCommandLine 直接启动 Fork 应用并传入仓库目录。
func forkAppCommandLine(appPath, folder string) *exec.Cmd {
	cmd := exec.Command(appPath, folder)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
