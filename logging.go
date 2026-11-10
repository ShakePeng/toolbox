package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// 单个日志文件的上限。超过后下一次启动时把旧文件改名为 .1，
// 只保留一份历史，避免长期运行时日志无限增长。
const logMaxBytes = 2 << 20

// setupLogging 把标准日志同时写到应用日志目录。
//
// 打包后的 Windows 版本使用 GUI 子系统，没有控制台，stderr 会被系统丢弃；
// 不落盘的话“设置 → 打开日志”打开的目录永远是空的，线上问题也无从排查。
func setupLogging() {
	dir, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		return
	}
	path := filepath.Join(dir, "toolbox.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > logMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	// 文件放在前面：MultiWriter 遇到第一个写入错误就会停止，
	// Windows GUI 进程写 stderr 会失败，不能让它挡住文件写入。
	log.SetOutput(io.MultiWriter(f, os.Stderr))
}
