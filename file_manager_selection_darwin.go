//go:build darwin

package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/ebitengine/purego/objc"
)

// finderSelectionScript 读取 Finder 当前选区的 POSIX 路径，每行一个。
// 不用 "selection as alias list"：空选区时该强制转换在部分系统版本上
// 会直接报错；先取原始 selection 再逐项转，空选区返回空串。
const finderSelectionScript = `tell application "Finder"
	set sel to selection
	if sel is {} then return ""
	set out to ""
	repeat with f in sel
		set out to out & (POSIX path of (f as alias)) & linefeed
	end repeat
	return out
end tell`

// fileManagerSelection 读取 Finder 选区。window 参数在 macOS 不用：
// Finder 的选区是全局状态，不挂在具体窗口上。
// 首次运行会触发系统的「控制 Finder」自动化授权弹窗，超时给足 10 秒
// 让用户读完并确认；整个调用发生在后台 goroutine，不阻塞 UI。
func fileManagerSelection(_ uintptr) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", finderSelectionScript).Output()
	if err != nil {
		return nil, fmt.Errorf("osascript: %w", err)
	}
	return parseFileManagerSelectionLines(string(out)), nil
}

// fileManagerForeground 判断唤起瞬间的前台应用是否 Finder。
// Finder 选区不依赖前台（osascript 查的是 Finder 自身状态），这里只做
// 门禁：非 Finder 前台时不带入，避免把 Finder 里残留的旧选区静默带进队列。
func fileManagerForeground() (bool, uintptr) {
	return frontmostBundleID() == "com.apple.finder", 0
}

// frontmostBundleID 用 NSWorkspace 取前台应用的 bundle id，
// 写法复用 selection_darwin.go frontmostPID 的 purego 模式。
func frontmostBundleID() string {
	cls := objc.GetClass("NSWorkspace")
	if cls == 0 {
		return ""
	}
	ws := objc.ID(cls).Send(objc.RegisterName("sharedWorkspace"))
	if ws == 0 {
		return ""
	}
	front := ws.Send(objc.RegisterName("frontmostApplication"))
	if front == 0 {
		return ""
	}
	bid := front.Send(objc.RegisterName("bundleIdentifier"))
	if bid == 0 {
		return ""
	}
	return objc.Send[string](bid, objc.RegisterName("UTF8String"))
}
