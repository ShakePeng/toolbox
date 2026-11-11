//go:build !windows

package main

// 其他平台没有 Windows 11 的 DWM 圆角描边问题。
func hideNativeWindowBorder(handle uintptr) {}
