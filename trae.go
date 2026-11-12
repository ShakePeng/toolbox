package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Trae 与 VSCode 同构（自带 bin/trae 命令行），探测和启动方式保持一致。
// 串行化探测，避免连续输入触发词时重复创建同一个快捷命令。
var traeProbeMu sync.Mutex

// traeProbe 是一次命令探测的结果。Resolved 是实际用来启动 Trae 的命令
// 或绝对路径；Status 是给界面看的说明；Err 表示不可用。
type traeProbe struct {
	Resolved string
	Status   string
	Err      error
}

// defaultTraeCommand 是实际用于检测和打开项目的命令。
// 设置页里可编辑的只是触发词，不参与探测。
const defaultTraeCommand = "trae"

// traeCommand 返回用户配置的触发词；留空时退回 trae。
func (a *app) traeCommand() string {
	if a == nil {
		return "trae"
	}
	// 配置被手工改成内置命令时退回默认值，否则「命令 + 空格」永远进不了
	// 项目模式。
	cmd := strings.TrimSpace(a.settings.LauncherTraeCommand)
	if cmd == "" || isBuiltinCommand(cmd) {
		return "trae"
	}
	return cmd
}

// probeTraeCommand 先查 PATH，再在常见安装位置找 Trae 自带的 CLI。
// 找到但不在 PATH 时，会在用户目录下配置一个快捷命令，避免修改系统 PATH。
func probeTraeCommand(command string) traeProbe {
	command = strings.TrimSpace(command)
	if command == "" {
		command = defaultTraeCommand
	}
	if path, err := exec.LookPath(command); err == nil {
		return traeProbe{Resolved: path, Status: "已检测到命令：" + path}
	}
	if looksLikeVSCodePath(command) {
		if info, err := os.Stat(command); err == nil && !info.IsDir() {
			return traeProbe{Resolved: command, Status: "已检测到命令：" + command}
		}
	}

	base := strings.TrimSuffix(strings.ToLower(filepath.Base(command)), filepath.Ext(command))
	if base != "trae" {
		return traeProbe{Err: fmt.Errorf("未找到命令 %q，请检查命令名称是否正确", command)}
	}

	candidate, err := findTraeCLI()
	if err != nil {
		return traeProbe{Err: err}
	}
	shim, installErr := installVSCodeShim(base, candidate)
	if installErr != nil || shim == "" {
		return traeProbe{
			Resolved: candidate,
			Status:   "已直接使用 Trae 安装路径：" + candidate,
		}
	}
	return traeProbe{Resolved: shim, Status: "已自动配置快捷命令：" + shim}
}

// findTraeCLI 按平台查找 Trae 的命令行入口。
func findTraeCLI() (string, error) {
	var candidates []string

	switch runtime.GOOS {
	case "windows":
		for _, root := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if strings.TrimSpace(root) == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, "Programs", "Trae", "bin", "trae.cmd"),
				filepath.Join(root, "Trae", "bin", "trae.cmd"),
				filepath.Join(root, "Trae", "Trae.exe"),
			)
		}
	case "darwin":
		candidates = append(candidates,
			filepath.Join("/Applications", "Trae.app", "Contents", "Resources", "app", "bin", "trae"),
		)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, "Applications", "Trae.app", "Contents", "Resources", "app", "bin", "trae"),
			)
		}
	}

	for _, path := range candidates {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	if runtime.GOOS == "darwin" {
		return "", errors.New("未检测到 Trae，请确认已安装 Trae 应用")
	}
	return "", errors.New("未检测到 Trae 安装，请先安装 Trae，或把命令改成实际的 Trae 可执行文件")
}

// openFolderInTrae 用解析后的命令打开项目目录。
func (a *app) openFolderInTrae(folder string) error {
	command := a.trae.effective
	if command == "" {
		probe := probeTraeCommand(defaultTraeCommand)
		if probe.Err != nil {
			return probe.Err
		}
		command = probe.Resolved
	}
	return launchTrae(command, folder)
}

func launchTrae(command, folder string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("Trae 命令为空")
	}
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("项目文件夹不存在：%s", folder)
	}
	cmd := vscodeCommandLine(command, folder)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Trae 失败：%w", err)
	}
	// 回收子进程，避免僵尸进程；启动器不等待编辑器退出。
	go func() { _ = cmd.Wait() }()
	return nil
}

// ---------------- 应用级探测状态 ----------------

type traeState struct {
	effective string
	status    string
	err       string
	checking  bool
	checked   bool
	gen       int
}

// detectTraeCommand 在后台检测命令并自动配置。结果通过 win.Update 回主线程。
func (a *app) detectTraeCommand() {
	if a == nil || !a.settings.LauncherTraeEnabled {
		a.trae = traeState{}
		a.refreshMain()
		if a != nil && a.launcher != nil {
			a.launcher.vscodeChanged()
		}
		return
	}
	command := defaultTraeCommand
	a.trae.gen++
	gen := a.trae.gen
	a.trae.checking = true
	a.trae.checked = false
	a.trae.status = "正在检测命令…"
	a.trae.err = ""
	a.refreshMain()

	// 无窗口（单元测试）时不做真实探测，避免依赖本机环境。
	if a.win == nil {
		a.trae.checking = false
		a.trae.checked = true
		a.trae.status = "尚未检测"
		return
	}
	go func() {
		traeProbeMu.Lock()
		probe := probeTraeCommand(command)
		traeProbeMu.Unlock()
		a.win.Update(func() {
			if a.quitting || a.trae.gen != gen {
				return
			}
			a.trae.checking = false
			a.trae.checked = true
			a.trae.effective = probe.Resolved
			a.trae.status = probe.Status
			if probe.Err != nil {
				a.trae.err = probe.Err.Error()
				a.trae.status = ""
			}
			if a.launcher != nil {
				a.launcher.vscodeChanged()
			}
		})
	}()
}

// ensureTraeProbe 只在已启用且尚未得到结果时触发检测。
func (a *app) ensureTraeProbe() {
	if a == nil || !a.settings.LauncherTraeEnabled {
		return
	}
	if a.trae.effective != "" || a.trae.checking || a.trae.checked {
		return
	}
	a.detectTraeCommand()
}

func (a *app) traeStatusText() string {
	if !a.settings.LauncherTraeEnabled {
		return "未开启"
	}
	if a.trae.checking {
		return "正在检测命令…"
	}
	if a.trae.err != "" {
		return "检测失败：" + a.trae.err
	}
	if a.trae.status != "" {
		return a.trae.status
	}
	return "尚未检测，点击“重新检测”"
}

func (a *app) traeIsReady() bool {
	return a != nil && a.trae.effective != "" && a.trae.err == ""
}
