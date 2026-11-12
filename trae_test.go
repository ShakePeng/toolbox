package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProbeTraeCommandUnknown(t *testing.T) {
	probe := probeTraeCommand("definitely-not-a-toolbox-command")
	if probe.Err == nil {
		t.Errorf("未知命令应返回错误，实际 %+v", probe)
	}
}

// 本机装了 Trae 的 trae 命令时，探测应直接返回 PATH 里的可执行文件；
// CI 没有安装时跳过，不把环境差异变成失败。
func TestProbeTraeCommandFindsInstalledTrae(t *testing.T) {
	if _, err := exec.LookPath("trae"); err != nil {
		t.Skip("本机没有 trae 命令")
	}
	probe := probeTraeCommand("trae")
	if probe.Err != nil || probe.Resolved == "" {
		t.Fatalf("应检测到 trae：%+v", probe)
	}
}

func TestTraeCommandDefault(t *testing.T) {
	a := newTestApp()
	if got := a.traeCommand(); got != "trae" {
		t.Errorf("默认命令应为 trae，实际 %q", got)
	}
	a.settings.LauncherTraeCommand = "  "
	if got := a.traeCommand(); got != "trae" {
		t.Errorf("空命令应回退 trae，实际 %q", got)
	}
	a.settings.LauncherTraeCommand = "trae-ai"
	if got := a.traeCommand(); got != "trae-ai" {
		t.Errorf("自定义命令不应被改写，实际 %q", got)
	}
	// 内置命令不能用作触发词。
	a.settings.LauncherTraeCommand = "reindex"
	if got := a.traeCommand(); got != "trae" {
		t.Errorf("与内置命令冲突时应回退 trae，实际 %q", got)
	}
}

func TestFindTraeCLIOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 路径测试")
	}
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	cli := filepath.Join(local, "Programs", "Trae", "bin", "trae.cmd")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findTraeCLI()
	if err != nil {
		t.Fatal(err)
	}
	if got != cli {
		t.Errorf("应找到 %q，实际 %q", cli, got)
	}
}

func TestLaunchTraeRejectsMissingFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-project")
	if err := launchTrae("trae", missing); err == nil {
		t.Error("不存在的项目文件夹应返回错误")
	}
}

// Trae 项目模式应和 VS Code 一样列出项目，并调用 Trae 打开。
func TestLauncherTraeProjectMode(t *testing.T) {
	s, _ := newTestProjectLauncher()
	s.app.settings.LauncherTraeEnabled = true
	s.app.settings.LauncherTraeCommand = "trae"
	s.app.settings.LauncherTraeFolders = []string{"/projects"}
	s.projectsLoaded = true
	s.projectRootsSig = "trae\x00/projects"
	launched := ""
	s.launchTraeProject = func(path string) error {
		launched = path
		return nil
	}

	s.query = "tra "
	s.rebuildResults()
	if len(s.results) != 2 || s.results[0].kind != launcherKindProject {
		t.Fatalf("Trae 项目模式应列出全部项目，实际 %+v", s.results)
	}
	if s.projectSource != "trae" {
		t.Fatalf("当前项目源应为 trae，实际 %q", s.projectSource)
	}
	s.activate(0)
	if launched != "/projects/alpha" {
		t.Fatalf("应用 Trae 打开 /projects/alpha，实际 %q", launched)
	}
}
