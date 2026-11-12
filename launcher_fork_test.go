package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Fork 项目模式：输入 fork + 空格进入，只匹配自己配置的前缀。
func TestProjectQueryFork(t *testing.T) {
	a := newTestApp()
	a.settings.LauncherForkEnabled = true
	a.settings.LauncherForkCommand = "fork"
	s := &launcherSession{app: a}

	s.query = "fork alpha"
	src, query, ok := s.projectQuery()
	if !ok || query != "alpha" {
		t.Fatalf("fork + 空格应进入项目模式，实际 ok=%v query=%q", ok, query)
	}
	if src.id != "fork" || !src.gitOnly {
		t.Errorf("应匹配 Fork 源且只列 Git 仓库：%+v", src)
	}

	s.query = "code alpha"
	if _, _, ok := s.projectQuery(); ok {
		t.Error("未启用 VS Code 时 code 前缀不应进入项目模式")
	}
}

// Fork 只列 Git 仓库：.git 目录和 worktree 的 .git 文件都算，普通目录跳过。
func TestScanProjectFoldersGitOnly(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"plain", "repo", filepath.Join("repo", ".git"), "worktree"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "worktree", ".git"), []byte("gitdir: x"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects, _ := scanProjectFolders([]string{root}, 100, true)
	var names []string
	for _, p := range projects {
		names = append(names, p.name)
	}
	if got, want := strings.Join(names, ","), "repo,worktree"; got != want {
		t.Errorf("只应列出 Git 仓库 %q，实际 %q", want, got)
	}
}

// 项目模式下按当前源选择打开方式：Fork 用 launchForkProject。
func TestActivateProjectUsesForkLauncher(t *testing.T) {
	s, vscodeLaunched, _ := newTestLauncherSession()
	s.app.settings.LauncherForkEnabled = true
	forkLaunched := ""
	s.launchForkProject = func(path string) error {
		forkLaunched = path
		return nil
	}
	s.results = []launcherItem{{kind: launcherKindProject, title: "Alpha", path: "/projects/alpha"}}

	s.projectSource = "fork"
	s.activate(0)
	if forkLaunched != "/projects/alpha" {
		t.Fatalf("Fork 模式应调用 Fork 打开，实际 %q", forkLaunched)
	}
	if *vscodeLaunched != "" {
		t.Errorf("Fork 模式不应调用 VS Code，实际 %q", *vscodeLaunched)
	}
}

// Fork 配置完全独立：两个源的抽屉可以同时展开，键名不能冲突。
func TestForkSettingsCard(t *testing.T) {
	a := newTestApp()
	a.settings.LauncherEnabled = true
	a.settings.LauncherForkEnabled = true
	a.settings.LauncherForkCommand = "fork"
	a.settings.LauncherForkFolders = []string{"/repos"}

	// Windows 字体行高比 macOS 大，760 高时 Fork 卡片刚好压在窗口底边外，
	// 这里给足高度，避免测试因平台字体度量失败。
	tt := ui.NewTester(a.view, 1080, 900)
	if err := tt.Click("快速启动"); err != nil {
		t.Fatalf("点击“快速启动”失败：%v", err)
	}
	if !tt.HasText("Fork 快速打开") {
		t.Fatalf("启动项应显示 Fork 卡片，实际文本：%q", tt.Texts())
	}
	if err := tt.Click("展开或收起 Fork 快速打开"); err != nil {
		t.Fatalf("展开 Fork 卡片失败：%v", err)
	}
	for _, want := range []string{"触发词", "项目文件夹", "/repos"} {
		if !tt.HasText(want) {
			t.Errorf("Fork 抽屉应显示 %q，实际文本：%q", want, tt.Texts())
		}
	}
}
