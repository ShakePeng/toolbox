//go:build darwin

package main

import "github.com/ebitengine/purego"

// 通过 CoreGraphics 检测/申请“屏幕录制”权限，避免 screencapture 在无权限时
// 静默只抓到桌面壁纸（看起来像所有窗口消失）。
var (
	cgPreflightScreenCapture func() uint8
	cgRequestScreenCapture   func() uint8
)

func init() {
	lib, err := purego.Dlopen(
		"/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics",
		purego.RTLD_LAZY|purego.RTLD_GLOBAL,
	)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&cgPreflightScreenCapture, lib, "CGPreflightScreenCaptureAccess")
	purego.RegisterLibFunc(&cgRequestScreenCapture, lib, "CGRequestScreenCaptureAccess")
}

// screenRecordingGranted 返回当前是否已获得屏幕录制权限。
func screenRecordingGranted() bool {
	return cgPreflightScreenCapture != nil && cgPreflightScreenCapture() != 0
}

// requestScreenRecording 触发系统授权弹窗（并把本程序加入“屏幕录制”列表）。
func requestScreenRecording() bool {
	if cgRequestScreenCapture == nil {
		return false
	}
	return cgRequestScreenCapture() != 0
}
