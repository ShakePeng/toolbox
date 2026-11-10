//go:build darwin

package main

import (
	"sync"

	"github.com/ebitengine/purego/objc"
)

// coverWindowLevel 用屏幕保护程序的层级（1000），高于菜单栏（24）和程序坞（20）。
const coverWindowLevel = 1000

const (
	styleTitledBit   = uint64(1 << 0)
	styleFullSizeBit = uint64(1 << 15)
)

var keyCapableOnce sync.Once

// coverSystemUI 让原生窗口（NSWindow*）真正铺满整块屏幕：
// 去掉标题栏样式（否则 AppKit 会把窗口限制在菜单栏下方），
// 并把层级提到菜单栏和程序坞之上。
func coverSystemUI(handle uintptr) {
	if handle == 0 {
		return
	}
	win := objc.ID(handle)
	// 无边框窗口默认不能成为 key window，键盘事件就收不到（Esc 会失效）；
	// 给这个类补一个 canBecomeKeyWindow 返回 YES。
	keyCapableOnce.Do(func() {
		imp := objc.NewIMP(func(objc.ID, objc.SEL) bool { return true })
		win.Class().AddMethod(objc.RegisterName("canBecomeKeyWindow"), imp, "B@:")
	})
	mask := objc.Send[uint64](win, objc.RegisterName("styleMask"))
	mask &^= styleTitledBit | styleFullSizeBit
	objc.Send[objc.ID](win, objc.RegisterName("setStyleMask:"), mask)
	objc.Send[objc.ID](win, objc.RegisterName("setLevel:"), coverWindowLevel)
}

// focusWindowContent 把窗口内容视图（mygo 的 surface）设成第一响应者，
// 这样不点鼠标也能直接收到键盘事件。
func focusWindowContent(handle uintptr) {
	if handle == 0 {
		return
	}
	win := objc.ID(handle)
	content := objc.Send[objc.ID](win, objc.RegisterName("contentView"))
	if content == 0 {
		return
	}
	subviews := objc.Send[objc.ID](content, objc.RegisterName("subviews"))
	if subviews == 0 {
		return
	}
	first := objc.Send[objc.ID](subviews, objc.RegisterName("firstObject"))
	if first == 0 {
		return
	}
	objc.Send[objc.ID](win, objc.RegisterName("makeFirstResponder:"), first)
}
