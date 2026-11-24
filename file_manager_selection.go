package main

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

// fetchFileManagerSelection 允许测试替换（对齐 ai_suppliers.go 的
// fetchAISupplierModels 先例），默认走平台实现（Finder / 资源管理器）。
var fetchFileManagerSelection = fileManagerSelection

// enqueueFileManagerSelection 是 tiny 命令 Run 里 goroutine 的函数体：
// 后台读取文件管理器选区，再回主线程入队。
//
// 调用顺序上 Run 必须先 showImageCompressor 再起 goroutine——a.compressor
// 的赋值发生在 go 语句之前，goroutine 读到的一定是已建好的会话；会话
// 常驻不置空（窗口关闭只是 Hide），不存在读到半初始化状态的问题。
func (a *app) enqueueFileManagerSelection(window uintptr) {
	paths, err := fetchFileManagerSelection(window)
	if a.quitting {
		return
	}
	c := a.compressor
	if c == nil {
		return
	}
	apply := func() {
		if err != nil {
			// 授权被拒等场景：压缩窗口已经打开，只在队列区提示即可，
			// 不再额外弹 toast 打断用户。
			c.notice = "读取文件管理器选区失败：" + err.Error()
			return
		}
		if len(paths) == 0 {
			return
		}
		// 复用 addPaths 的格式白名单 / 去重 / stat 校验，只入队不自动压缩。
		c.addPaths(paths)
	}
	if c.win != nil {
		c.win.Update(apply)
	} else {
		// 单测里手工构造的会话没有窗口，直接在当前线程执行。
		apply()
	}
}

// parseFileManagerSelectionLines 解析换行分隔的路径输出。osascript 与
// PowerShell 的 Write-Output 都是每行一个路径，行尾可能是 \n 或 \r\n，
// 统一 TrimSpace 处理；空输出返回 nil。
func parseFileManagerSelectionLines(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

// powerShellEncodedCommand 把脚本文本转成 powershell.exe -EncodedCommand
// 参数：UTF-16LE 字节流的 base64。用编码命令而不是拼命令行字符串，
// 彻底绕开引号与中文路径的转义问题。
func powerShellEncodedCommand(script string) string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		// UTF-16LE：低字节在前。
		buf[i*2] = byte(u)
		buf[i*2+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(buf)
}
