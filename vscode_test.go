package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProbeVSCodeCommandUnknown(t *testing.T) {
	probe := probeVSCodeCommand("definitely-not-a-toolbox-command")
	if probe.Err == nil {
		t.Errorf("未知命令应返回错误，实际 %+v", probe)
	}
}

// 本机装了 VSCode 的 code 命令时，探测应直接返回 PATH 里的可执行文件；
// CI 没有安装时跳过，不把环境差异变成失败。
func TestProbeVSCodeCommandFindsInstalledCode(t *testing.T) {
	if _, err := exec.LookPath("code"); err != nil {
		t.Skip("本机没有 code 命令")
	}
	probe := probeVSCodeCommand("code")
	if probe.Err != nil || probe.Resolved == "" {
		t.Fatalf("应检测到 code：%+v", probe)
	}
}

func TestVSCodeCommandDefault(t *testing.T) {
	a := newTestApp()
	if got := a.vscodeCommand(); got != "code" {
		t.Errorf("默认命令应为 code，实际 %q", got)
	}
	a.settings.LauncherVSCodeCommand = "  "
	if got := a.vscodeCommand(); got != "code" {
		t.Errorf("空命令应回退 code，实际 %q", got)
	}
	a.settings.LauncherVSCodeCommand = "cursor"
	if got := a.vscodeCommand(); got != "cursor" {
		t.Errorf("自定义命令不应被改写，实际 %q", got)
	}
}

func TestInstallVSCodeShim(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cli := filepath.Join(home, "fake-vscode-cli")

	shim, err := installVSCodeShim("code-test", cli)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(shim, ".cmd") {
			t.Errorf("Windows shim 应为 .cmd，实际 %q", shim)
		}
		data, err := os.ReadFile(shim)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), cli) {
			t.Errorf("shim 应引用 %q，实际 %q", cli, string(data))
		}
	} else {
		target, err := os.Readlink(shim)
		if err != nil {
			t.Fatal(err)
		}
		if target != cli {
			t.Errorf("符号链接应指向 %q，实际 %q", cli, target)
		}
	}

	// 重复调用应复用已有的 shim，而不是报错。
	if again, err := installVSCodeShim("code-test", cli); err != nil || again != shim {
		t.Errorf("重复安装应复用 shim，实际 %q / %v", again, err)
	}
}

func TestInstallVSCodeShimDoesNotOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var path string
	if runtime.GOOS == "windows" {
		path = filepath.Join(home, ".toolbox", "bin", "code-other.cmd")
	} else {
		path = filepath.Join(home, ".local", "bin", "code-other")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installVSCodeShim("code-other", "/tmp/other-cli"); err == nil {
		t.Error("已存在的同名文件不应被覆盖")
	}
}

func TestFindVSCodeCLIOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 路径测试")
	}
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	cli := filepath.Join(local, "Programs", "Microsoft VS Code", "bin", "code.cmd")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findVSCodeCLI("code")
	if err != nil {
		t.Fatal(err)
	}
	if got != cli {
		t.Errorf("应找到 %q，实际 %q", cli, got)
	}
}

func TestLaunchVSCodeRejectsMissingFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-project")
	if err := launchVSCode("code", missing); err == nil {
		t.Error("不存在的项目文件夹应返回错误")
	}
}
