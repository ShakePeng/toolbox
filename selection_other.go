//go:build !darwin && !windows

package main

import "errors"

// readSelectedText 在尚未适配的平台直接报错。
// macOS 用辅助功能、Windows 用模拟 Ctrl+C，Linux 的两种方式都还没做。
func readSelectedText() (string, error) {
	return "", errors.New("当前平台暂不支持读取选中文本")
}
