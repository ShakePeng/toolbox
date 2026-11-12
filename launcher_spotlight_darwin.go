//go:build darwin

package main

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

// spotlightAppNames 批量读取 Spotlight 的应用显示名（kMDItemDisplayName）。
// 系统自带应用（终端、系统设置、计算器）的中文名既不在 Info.plist，也不在
// InfoPlist.strings 里，只有 Spotlight 索引与 Finder/Launchpad 显示的名字一致。
// mdls 只查已有索引、不做全盘扫描；索引不可用时返回空，调用方回退系统名。
func spotlightAppNames(paths []string) map[string]string {
	if len(paths) == 0 {
		return nil
	}
	// mdls 每次启动只跑一次；加超时避免索引异常时卡住应用枚举。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := append([]string{"-name", "kMDItemDisplayName"}, paths...)
	out, err := exec.CommandContext(ctx, "/usr/bin/mdls", args...).Output()
	// 个别应用查不到时 mdls 会以非零状态退出，但其余名字仍可用，
	// 所以只在完全没有输出时才整体回退。
	if len(out) == 0 {
		if err != nil {
			log.Printf("读取 Spotlight 应用名失败：%v", err)
		}
		return nil
	}
	return parseSpotlightNames(string(out), paths)
}

// parseSpotlightNames 按输出顺序把名字对应回路径。mdls 每个文件输出一行，
// 属性缺失时是 (null)；不能只对成功行计数，否则名字会错位到其他应用。
func parseSpotlightNames(out string, paths []string) map[string]string {
	names := make(map[string]string, len(paths))
	idx := 0
	for _, line := range strings.Split(out, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "kMDItemDisplayName = ")
		if !ok {
			continue
		}
		if idx >= len(paths) {
			break
		}
		path := paths[idx]
		idx++
		if value == "(null)" {
			continue
		}
		if name := strings.Trim(strings.TrimSpace(value), `"`); name != "" {
			names[path] = name
		}
	}
	return names
}
