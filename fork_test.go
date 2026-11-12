package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Fork 2.x 用 Velopack 安装，可执行文件在 current 子目录；旧版 Squirrel
// 才是 Fork\Fork.exe。两种布局都要能识别。
func TestFindForkAppWindowsLayouts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 路径测试")
	}
	cases := []struct {
		name string
		rel  string
	}{
		{"Velopack", filepath.Join("Fork", "current", "Fork.exe")},
		{"Squirrel", filepath.Join("Fork", "Fork.exe")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := t.TempDir()
			t.Setenv("LOCALAPPDATA", local)
			// 屏蔽 Program Files 里的真实安装，避免测试读到本机环境。
			t.Setenv("ProgramFiles", filepath.Join(local, "missing-pf"))
			t.Setenv("ProgramFiles(x86)", filepath.Join(local, "missing-pf86"))

			exe := filepath.Join(local, tc.rel)
			if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exe, []byte("stub"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := findForkApp(); got != exe {
				t.Errorf("应找到 %q，实际 %q", exe, got)
			}
		})
	}
}

// Windows 版 Fork 没有独立 CLI，启动 exe 时只能直接传仓库路径。
func TestForkCommandLineWindowsArgs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 启动参数测试")
	}
	exe := filepath.Join(`C:\Tools`, "Fork.exe")
	folder := filepath.Join(`D:\repos`, "alpha")

	for name, cmd := range map[string]struct {
		args []string
	}{
		"CLI":  {forkCommandLine(exe, folder).Args},
		"应用本体": {forkAppCommandLine(exe, folder).Args},
	} {
		if len(cmd.args) != 2 || cmd.args[0] != exe || cmd.args[1] != folder {
			t.Errorf("%s 应直接传仓库路径，实际参数 %q", name, cmd.args)
		}
	}
}
