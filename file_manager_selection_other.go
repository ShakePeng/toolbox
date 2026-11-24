//go:build !darwin && !windows

package main

import "errors"

// 该平台没有实现文件管理器选区读取，与 selection_other.go 的处理一致：
// 前台判断恒 false，图片压缩触发词退化为只打开压缩窗口。
func fileManagerForeground() (bool, uintptr) {
	return false, 0
}

func fileManagerSelection(uintptr) ([]string, error) {
	return nil, errors.New("该平台暂无文件管理器选区支持")
}
