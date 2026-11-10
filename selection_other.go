//go:build !darwin

package main

import "errors"

// readSelectedText 在非 macOS 平台暂未实现。
// 划词依赖系统的选区接口，Windows/Linux 需要各自的实现，后续按需补充。
func readSelectedText() (string, error) {
	return "", errors.New("当前平台暂不支持读取选中文本")
}
