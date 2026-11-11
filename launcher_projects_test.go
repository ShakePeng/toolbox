package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newProjectTestSession(command string) *launcherSession {
	a := newTestApp()
	a.settings.LauncherVSCodeEnabled = true
	a.settings.LauncherVSCodeCommand = command
	return &launcherSession{app: a}
}

func TestProjectQuery(t *testing.T) {
	cases := []struct {
		command string
		query   string
		want    string
		ok      bool
	}{
		{"code", "code ", "", true},
		{"code", "code alpha", "alpha", true},
		{"code", "CODE alpha", "alpha", true},
		{"code", "code\talpha", "alpha", true},
		{"code", "code", "", false},
		{"code", "codefoo", "", false},
		{"cursor", "cursor app", "app", true},
		{"cursor", "code app", "", false},
	}
	for _, c := range cases {
		s := newProjectTestSession(c.command)
		s.query = c.query
		got, ok := s.projectQuery()
		if got != c.want || ok != c.ok {
			t.Errorf("command=%q query=%q → (%q,%v)，期望 (%q,%v)", c.command, c.query, got, ok, c.want, c.ok)
		}
	}

	s := newProjectTestSession("code")
	s.app.settings.LauncherVSCodeEnabled = false
	s.query = "code alpha"
	if _, ok := s.projectQuery(); ok {
		t.Error("功能关闭时不应进入项目模式")
	}
}

func TestScanProjectFolders(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	for _, dir := range []string{"alpha", "Beta", ".hidden", filepath.Join("alpha", "nested")} {
		if err := os.MkdirAll(filepath.Join(root1, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root2, "gamma"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root1, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(root2, "does-not-exist")
	projects, errs := scanProjectFolders([]string{root1, root2, missing}, 100)

	var names []string
	for _, p := range projects {
		names = append(names, p.name)
	}
	if got, want := strings.Join(names, ","), "alpha,Beta,gamma"; got != want {
		t.Errorf("项目应按名称排序为 %q，实际 %q", want, got)
	}
	for _, bad := range []string{".hidden", "notes.txt", "nested"} {
		for _, p := range projects {
			if p.name == bad {
				t.Errorf("不应收录 %q", bad)
			}
		}
	}
	if len(errs) != 1 {
		t.Errorf("缺失目录应产生 1 条错误，实际 %d：%v", len(errs), errs)
	}
}

func TestScanProjectFoldersLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projects, _ := scanProjectFolders([]string{root}, 2)
	if len(projects) != 2 {
		t.Fatalf("应限制为 2 个，实际 %d", len(projects))
	}
}

func TestFilterLauncherProjects(t *testing.T) {
	projects := []launcherProjectEntry{
		{path: "/p/alpha", name: "Alpha", root: "/p", nameLower: "alpha", pathLower: "/p/alpha"},
		{path: "/p/beta", name: "Beta", root: "/p", nameLower: "beta", pathLower: "/p/beta"},
		{path: "/p/gamma", name: "Gamma", root: "/p", nameLower: "gamma", pathLower: "/p/gamma"},
	}

	items := filterLauncherProjects("al", projects, 8)
	if len(items) != 1 || items[0].title != "Alpha" || items[0].kind != launcherKindProject {
		t.Fatalf("应只匹配 Alpha：%+v", items)
	}
	if items[0].subtitle != "/p" {
		t.Errorf("副标题应为项目根目录，实际 %q", items[0].subtitle)
	}

	items = filterLauncherProjects("", projects, 2)
	if len(items) != 2 || items[0].title != "Alpha" || items[1].title != "Beta" {
		t.Fatalf("空查询应列出前两个项目：%+v", items)
	}

	items = filterLauncherProjects("zzz", projects, 8)
	if len(items) != 0 {
		t.Errorf("不匹配时应为空，实际 %+v", items)
	}
}

func TestNormalizeProjectFolders(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("没有用户目录")
	}
	got := normalizeProjectFolders([]string{"~", "~/work", "~/work", " "})
	if len(got) != 2 {
		t.Fatalf("应去重并展开 ~，实际 %v", got)
	}
	if got[0] != filepath.Clean(home) {
		t.Errorf("~ 应展开为 %q，实际 %q", home, got[0])
	}
	if got[1] != filepath.Join(home, "work") {
		t.Errorf("~/work 应展开为 %q，实际 %q", filepath.Join(home, "work"), got[1])
	}
}
