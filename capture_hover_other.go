//go:build !darwin && !windows

package main

import "github.com/egoist/mygo"

// listCaptureWindowsPlatform 在其他平台没有窗口枚举实现，返回 nil 让悬停选窗
// 静默失效（这些平台本来也没有截图实现）。
func listCaptureWindowsPlatform(_ mygo.Display) []mygo.Rectangle {
	return nil
}
