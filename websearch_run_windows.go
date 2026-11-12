//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// webSearchCommand 通过 URL 协议处理器打开默认浏览器。rundll32 不会
// 弹控制台；HideWindow 再兜一层，避免启动器闪黑框。
func webSearchCommand(rawURL string) *exec.Cmd {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
