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

// 命令探测可能因为输入命令时连续按键而被触发多次。串行化探测，避免多个
// goroutine 同时创建同一个快捷命令。
var vscodeProbeMu sync.Mutex

// vscodeProbe 是一次命令探测的结果。Resolved 是实际用来启动 VSCode 的
// 命令或绝对路径；Status 是给界面看的说明；Err 表示不可用。
type vscodeProbe struct {
	Resolved string
	Status   string
	Err      error
}

// defaultVSCodeCommand 是实际用于检测和打开项目的命令。
// 设置页里可编辑的只是触发词，不参与探测。
const defaultVSCodeCommand = "code"

// vscodeCommand 返回用户配置的触发词；留空时退回 code。
func (a *app) vscodeCommand() string {
	if a == nil {
		return "code"
	}
	// 配置被手工改成内置命令时退回默认值，否则「命令 + 空格」永远进不了
	// 项目模式。
	cmd := strings.TrimSpace(a.settings.LauncherVSCodeCommand)
	if cmd == "" || isBuiltinCommand(cmd) {
		return "code"
	}
	return cmd
}

// probeVSCodeCommand 先查 PATH，再在常见安装位置找 VSCode 自带的 CLI。
// 找到但不在 PATH 时，会在用户目录下配置一个快捷命令，避免修改系统 PATH。
func probeVSCodeCommand(command string) vscodeProbe {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "code"
	}
	if path, err := exec.LookPath(command); err == nil {
		return vscodeProbe{Resolved: path, Status: "已检测到命令：" + path}
	}
	if looksLikeVSCodePath(command) {
		if info, err := os.Stat(command); err == nil && !info.IsDir() {
			return vscodeProbe{Resolved: command, Status: "已检测到命令：" + command}
		}
	}

	base := strings.TrimSuffix(strings.ToLower(filepath.Base(command)), filepath.Ext(command))
	if base != "code" && base != "code-insiders" {
		return vscodeProbe{Err: fmt.Errorf("未找到命令 %q，请检查命令名称是否正确", command)}
	}

	candidate, err := findVSCodeCLI(base)
	if err != nil {
		return vscodeProbe{Err: err}
	}
	shim, installErr := installVSCodeShim(base, candidate)
	if installErr != nil || shim == "" {
		return vscodeProbe{
			Resolved: candidate,
			Status:   "已直接使用 VSCode 安装路径：" + candidate,
		}
	}
	return vscodeProbe{Resolved: shim, Status: "已自动配置快捷命令：" + shim}
}

func looksLikeVSCodePath(command string) bool {
	return filepath.IsAbs(command) || strings.ContainsAny(command, `/\`)
}

// findVSCodeCLI 按平台查找 VSCode 的 CLI。base 只能是 code / code-insiders。
func findVSCodeCLI(base string) (string, error) {
	insiders := base == "code-insiders"
	var candidates []string

	switch runtime.GOOS {
	case "windows":
		family := "Microsoft VS Code"
		cli := "code.cmd"
		exe := "Code.exe"
		if insiders {
			family = "Microsoft VS Code Insiders"
			cli = "code-insiders.cmd"
			exe = "Code - Insiders.exe"
		}
		for _, base := range []struct{ root, sub string }{
			{os.Getenv("LOCALAPPDATA"), "Programs"},
			{os.Getenv("ProgramFiles"), ""},
			{os.Getenv("ProgramFiles(x86)"), ""},
		} {
			if strings.TrimSpace(base.root) == "" {
				continue
			}
			dir := filepath.Join(base.root, base.sub, family)
			candidates = append(candidates, filepath.Join(dir, "bin", cli), filepath.Join(dir, exe))
		}
	case "darwin":
		app := "Visual Studio Code.app"
		if insiders {
			app = "Visual Studio Code - Insiders.app"
		}
		candidates = append(candidates,
			filepath.Join("/Applications", app, "Contents", "Resources", "app", "bin", base),
		)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, "Applications", app, "Contents", "Resources", "app", "bin", base),
			)
		}
	default:
		candidates = append(candidates, "/usr/share/code/bin/"+base, "/usr/bin/"+base, "/snap/bin/"+base)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".local", "bin", base))
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
		return "", errors.New("未检测到 VSCode，请确认已安装 VSCode 应用")
	}
	return "", errors.New("未检测到 VSCode 安装，请先安装 VSCode，或把命令改成实际的 VSCode 可执行文件")
}

// installVSCodeShim 在用户目录下创建一个不覆盖现有文件的快捷命令。
// 启动器直接使用这个绝对路径，所以不需要改系统 PATH。
func installVSCodeShim(base, cli string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("无法定位用户目录")
	}

	if runtime.GOOS == "windows" {
		dir := filepath.Join(home, ".toolbox", "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		shim := filepath.Join(dir, base+".cmd")
		content := fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", cli)
		if old, err := os.ReadFile(shim); err == nil {
			if strings.Contains(string(old), cli) {
				return shim, nil
			}
			return "", fmt.Errorf("%s 已存在且不是本应用创建的快捷命令", shim)
		}
		if err := os.WriteFile(shim, []byte(content), 0o755); err != nil {
			return "", err
		}
		return shim, nil
	}

	dir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	shim := filepath.Join(dir, base)
	if target, err := os.Readlink(shim); err == nil {
		if target == cli {
			return shim, nil
		}
		return "", fmt.Errorf("%s 已指向 %s", shim, target)
	} else if _, statErr := os.Stat(shim); statErr == nil {
		return "", fmt.Errorf("%s 已存在且不是符号链接", shim)
	}
	if err := os.Symlink(cli, shim); err != nil {
		return "", err
	}
	return shim, nil
}

// openFolderInVSCode 用解析后的命令打开项目目录。
func (a *app) openFolderInVSCode(folder string) error {
	command := a.vscode.effective
	if command == "" {
		probe := probeVSCodeCommand(defaultVSCodeCommand)
		if probe.Err != nil {
			return probe.Err
		}
		command = probe.Resolved
	}
	return launchVSCode(command, folder)
}

func launchVSCode(command, folder string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("VSCode 命令为空")
	}
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("项目文件夹不存在：%s", folder)
	}
	cmd := vscodeCommandLine(command, folder)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 VSCode 失败：%w", err)
	}
	// 回收子进程，避免僵尸进程；启动器不等待编辑器退出。
	go func() { _ = cmd.Wait() }()
	return nil
}

// ---------------- 应用级探测状态 ----------------

type vscodeState struct {
	effective string
	status    string
	err       string
	checking  bool
	checked   bool
	gen       int
}

// detectVSCodeCommand 在后台检测命令并自动配置。结果通过 win.Update 回主线程。
func (a *app) detectVSCodeCommand() {
	if a == nil || !a.settings.LauncherVSCodeEnabled {
		a.vscode = vscodeState{}
		a.refreshMain()
		if a != nil && a.launcher != nil {
			a.launcher.vscodeChanged()
		}
		return
	}
	command := defaultVSCodeCommand
	a.vscode.gen++
	gen := a.vscode.gen
	a.vscode.checking = true
	a.vscode.checked = false
	a.vscode.status = "正在检测命令…"
	a.vscode.err = ""
	a.refreshMain()

	// 无窗口（单元测试）时不做真实探测，避免依赖本机环境。
	if a.win == nil {
		a.vscode.checking = false
		a.vscode.checked = true
		a.vscode.status = "尚未检测"
		return
	}
	go func() {
		vscodeProbeMu.Lock()
		probe := probeVSCodeCommand(command)
		vscodeProbeMu.Unlock()
		a.win.Update(func() {
			if a.quitting || a.vscode.gen != gen {
				return
			}
			a.vscode.checking = false
			a.vscode.checked = true
			a.vscode.effective = probe.Resolved
			a.vscode.status = probe.Status
			if probe.Err != nil {
				a.vscode.err = probe.Err.Error()
				a.vscode.status = ""
			}
			if a.launcher != nil {
				a.launcher.vscodeChanged()
			}
		})
	}()
}

// ensureVSCodeProbe 只在已启用且尚未得到结果时触发检测。
func (a *app) ensureVSCodeProbe() {
	if a == nil || !a.settings.LauncherVSCodeEnabled {
		return
	}
	if a.vscode.effective != "" || a.vscode.checking || a.vscode.checked {
		return
	}
	a.detectVSCodeCommand()
}

func (a *app) vscodeStatusText() string {
	if !a.settings.LauncherVSCodeEnabled {
		return "未开启"
	}
	if a.vscode.checking {
		return "正在检测命令…"
	}
	if a.vscode.err != "" {
		return "检测失败：" + a.vscode.err
	}
	if a.vscode.status != "" {
		return a.vscode.status
	}
	return "尚未检测，点击“重新检测”"
}

func (a *app) vscodeIsReady() bool {
	return a != nil && a.vscode.effective != "" && a.vscode.err == ""
}
