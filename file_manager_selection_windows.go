//go:build windows

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	fmUser32        = syscall.NewLazyDLL("user32.dll")
	fmGetForeground = fmUser32.NewProc("GetForegroundWindow")
	fmGetClassName  = fmUser32.NewProc("GetClassNameW")
)

// fileManagerForeground 在唤起启动器的瞬间判断前台窗口是否资源管理器，
// 并返回该窗口的 HWND。Explorer 的选区挂在具体窗口上，之后要靠 HWND
// 从 Shell.Application.Windows() 里找回同一窗口，多开窗口不会串。
// CabinetWClass 是普通资源管理器窗口，ExploreWClass 是旧式搜索窗口；
// 桌面（Progman/WorkerW）不在 Shell.Windows() 集合里，读不到选区，不算。
func fileManagerForeground() (bool, uintptr) {
	hwnd, _, _ := fmGetForeground.Call()
	if hwnd == 0 {
		return false, 0
	}
	buf := make([]uint16, 64)
	n, _, _ := fmGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	switch syscall.UTF16ToString(buf[:n]) {
	case "CabinetWClass", "ExploreWClass":
		return true, hwnd
	}
	return false, 0
}

// explorerSelectionTemplate 按 HWND 找回唤起时的资源管理器窗口并输出
// 其选中项路径，每行一个。先设 UTF-8 输出编码，否则重定向 stdout 走
// OEM 代码页，中文路径会乱码。
const explorerSelectionTemplate = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$shell = New-Object -ComObject Shell.Application
$win = $shell.Windows() | Where-Object { $_.HWND -eq {HWND} }
if ($win) { foreach ($i in $win.Document.SelectedItems()) { Write-Output $i.Path } }`

// fileManagerSelection 读取 HWND 对应资源管理器窗口的选中文件。
// 用 -EncodedCommand 传 UTF-16LE base64 脚本，避免命令行引号转义；
// 隐藏控制台窗口防止回车时闪黑框（fork_run_windows.go 同款惯例）。
func fileManagerSelection(hwnd uintptr) ([]string, error) {
	if hwnd == 0 {
		return nil, nil
	}
	script := strings.ReplaceAll(explorerSelectionTemplate, "{HWND}", strconv.FormatUint(uint64(hwnd), 10))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive",
		"-EncodedCommand", powerShellEncodedCommand(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("powershell: %w", err)
	}
	return parseFileManagerSelectionLines(string(out)), nil
}
