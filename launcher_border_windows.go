//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	dwmwaBorderColor = 34
	dwmColorNone     = 0xFFFFFFFE
)

var (
	dwmapiDLL                 = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = dwmapiDLL.NewProc("DwmSetWindowAttribute")
)

// hideNativeWindowBorder 去掉 Windows 11 给圆角浮窗描的 DWM 边框。
// MyGo 对 Panel/Frameless 窗口设置 DWM 圆角，DWM 会同时画一圈 1px 边框；
// UI 里再画 Border 就会变成两条平行边框。DWMWA_COLOR_NONE 只去掉描边，
// 圆角裁切和阴影仍然保留。
func hideNativeWindowBorder(handle uintptr) {
	if handle == 0 {
		return
	}
	color := uint32(dwmColorNone)
	procDwmSetWindowAttribute.Call(
		handle,
		dwmwaBorderColor,
		uintptr(unsafe.Pointer(&color)),
		unsafe.Sizeof(color),
	)
}
