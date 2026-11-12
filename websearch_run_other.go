//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

// webSearchCommand 用系统默认浏览器打开搜索地址。macOS 用 open，
// 其他 Unix 桌面用 xdg-open。
func webSearchCommand(rawURL string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", rawURL)
	}
	return exec.Command("xdg-open", rawURL)
}
