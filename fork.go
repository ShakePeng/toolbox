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

// Fork 的命令探测和 VSCode 分开：Fork 不一定安装命令行工具，
// 没有 CLI 时退回应用本体，用系统方式把仓库交给它打开。
var forkProbeMu sync.Mutex

// forkProbe 是一次 Fork 探测的结果。Resolved 是 CLI 路径；AppPath 是
// Fork 应用本体的路径，两者至少有一个才算可用。
type forkProbe struct {
	Resolved string
	AppPath  string
	Status   string
	Err      error
}

// defaultForkCommand 是实际用于检测和打开仓库的命令，
// 设置页里可编辑的只是触发词。
const defaultForkCommand = "fork"

// forkCommand 返回用户配置的触发词；留空时退回 fork。
func (a *app) forkCommand() string {
	if a == nil {
		return "fork"
	}
	// 配置被手工改成内置命令时退回默认值，否则「命令 + 空格」永远进不了
	// 项目模式。
	cmd := strings.TrimSpace(a.settings.LauncherForkCommand)
	if cmd == "" || isBuiltinCommand(cmd) {
		return "fork"
	}
	return cmd
}

// probeForkCommand 依次查找 CLI、常见安装位置和应用本体。
func probeForkCommand(command string) forkProbe {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "fork"
	}
	if looksLikeVSCodePath(command) {
		if info, err := os.Stat(command); err == nil && !info.IsDir() {
			return forkProbe{Resolved: command, Status: "已检测到命令：" + command}
		}
	} else if path, err := exec.LookPath(command); err == nil {
		return forkProbe{Resolved: path, Status: "已检测到命令：" + path}
	}
	for _, path := range forkCLICandidates() {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return forkProbe{Resolved: path, Status: "已检测到命令：" + path}
		}
	}
	if appPath := findForkApp(); appPath != "" {
		return forkProbe{
			AppPath: appPath,
			Status:  "未找到命令行工具，已改用 Fork 应用打开：" + appPath,
		}
	}
	if runtime.GOOS == "darwin" {
		return forkProbe{Err: errors.New("未检测到 Fork，请确认已安装 Fork")}
	}
	return forkProbe{Err: errors.New("未检测到 Fork，请确认已安装 Fork，或把命令改成 Fork 可执行文件")}
}

func forkCLICandidates() []string {
	if runtime.GOOS != "darwin" {
		// Windows 版 Fork 没有独立 CLI，Fork.exe 只能当应用本体由
		// findForkApp 处理；否则会走 macOS 的 -C 参数，打不开仓库。
		return nil
	}
	out := []string{"/usr/local/bin/fork", "/opt/homebrew/bin/fork"}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".local", "bin", "fork"))
	}
	return out
}

// findForkApp 返回 Fork 应用本体所在路径，找不到时为空。
func findForkApp() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Fork.app"}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, "Applications", "Fork.app"))
		}
	case "windows":
		// Fork 2.x 改用 Velopack 安装，可执行文件在 current 子目录；
		// 直接查 Fork\Fork.exe 是早期 Squirrel 布局，保留兼容。
		local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if local != "" {
			candidates = append(candidates,
				filepath.Join(local, "Fork", "current", "Fork.exe"),
				filepath.Join(local, "Fork", "Fork.exe"),
			)
		}
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if strings.TrimSpace(root) != "" {
				candidates = append(candidates, filepath.Join(root, "Fork", "Fork.exe"))
			}
		}
		// 兜底：Velopack 的版本目录名可能变化，扫一层子目录找 Fork.exe。
		if local != "" {
			if entries, err := os.ReadDir(filepath.Join(local, "Fork")); err == nil {
				for _, e := range entries {
					if e.IsDir() && e.Name() != "packages" {
						candidates = append(candidates, filepath.Join(local, "Fork", e.Name(), "Fork.exe"))
					}
				}
			}
		}
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// openFolderInFork 用 Fork 打开项目目录。只接受 Git 仓库，
// 项目模式据此只列含 .git 的目录。
func (a *app) openFolderInFork(folder string) error {
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("项目文件夹不存在：%s", folder)
	}
	if a.fork.effective != "" {
		return launchFork(a.fork.effective, folder)
	}
	if a.fork.appPath != "" {
		return openFolderInApp(a.fork.appPath, folder)
	}
	probe := probeForkCommand(defaultForkCommand)
	if probe.Err != nil {
		return probe.Err
	}
	if probe.Resolved != "" {
		return launchFork(probe.Resolved, folder)
	}
	return openFolderInApp(probe.AppPath, folder)
}

// launchFork 调用 Fork CLI 打开指定仓库。CLI 启动后立即返回，
// 不等待编辑器退出，并回收子进程避免僵尸。
func launchFork(command, folder string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("Fork 命令为空")
	}
	cmd := forkCommandLine(command, folder)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Fork 失败：%w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// openFolderInApp 在 macOS 用 open -a 把目录交给 Fork 应用；
// 其他平台直接启动可执行文件并传入目录。
func openFolderInApp(appPath, folder string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("/usr/bin/open", "-a", appPath, folder)
	} else {
		cmd = forkAppCommandLine(appPath, folder)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Fork 失败：%w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ---------------- 应用级探测状态 ----------------

type forkState struct {
	effective string
	appPath   string
	status    string
	err       string
	checking  bool
	checked   bool
	gen       int
}

// detectForkCommand 在后台检测 Fork，结果通过 win.Update 回主线程。
func (a *app) detectForkCommand() {
	if a == nil || !a.settings.LauncherForkEnabled {
		a.fork = forkState{}
		a.refreshMain()
		if a != nil && a.launcher != nil {
			a.launcher.vscodeChanged()
		}
		return
	}
	command := defaultForkCommand
	a.fork.gen++
	gen := a.fork.gen
	a.fork.checking = true
	a.fork.checked = false
	a.fork.status = "正在检测命令…"
	a.fork.err = ""
	a.refreshMain()

	// 无窗口（单元测试）时不做真实探测，避免依赖本机环境。
	if a.win == nil {
		a.fork.checking = false
		a.fork.checked = true
		a.fork.status = "尚未检测"
		return
	}
	go func() {
		forkProbeMu.Lock()
		probe := probeForkCommand(command)
		forkProbeMu.Unlock()
		a.win.Update(func() {
			if a.quitting || a.fork.gen != gen {
				return
			}
			a.fork.checking = false
			a.fork.checked = true
			a.fork.effective = probe.Resolved
			a.fork.appPath = probe.AppPath
			a.fork.status = probe.Status
			if probe.Err != nil {
				a.fork.err = probe.Err.Error()
				a.fork.status = ""
			}
			if a.launcher != nil {
				a.launcher.vscodeChanged()
			}
		})
	}()
}

// ensureForkProbe 只在已启用且尚未得到结果时触发检测。
func (a *app) ensureForkProbe() {
	if a == nil || !a.settings.LauncherForkEnabled {
		return
	}
	if a.fork.effective != "" || a.fork.appPath != "" || a.fork.checking || a.fork.checked {
		return
	}
	a.detectForkCommand()
}

func (a *app) forkStatusText() string {
	if !a.settings.LauncherForkEnabled {
		return "未开启"
	}
	if a.fork.checking {
		return "正在检测命令…"
	}
	if a.fork.err != "" {
		return "检测失败：" + a.fork.err
	}
	if a.fork.status != "" {
		return a.fork.status
	}
	return "尚未检测，点击“重新检测”"
}

func (a *app) forkIsReady() bool {
	return a != nil && (a.fork.effective != "" || a.fork.appPath != "") && a.fork.err == ""
}
