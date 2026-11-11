package main

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestLauncherScoreTiers(t *testing.T) {
	cases := []struct {
		term, field string
		want        int
	}{
		{"code", "code", 1000},
		{"vis", "visual studio code", 800},
		{"code", "visual studio code", 586},
		{"isual", "visual studio code", 399},
		{"doc", "my_document", 597},
		{"zzz", "visual studio code", -1},
	}
	for _, c := range cases {
		if got := launcherFieldScore(c.term, c.field); got != c.want {
			t.Errorf("launcherFieldScore(%q, %q) = %d，期望 %d", c.term, c.field, got, c.want)
		}
	}
}

func TestLauncherScoreCaseAndPathFallback(t *testing.T) {
	// launcherFieldScore 的字段在生产路径里已经小写化；大小写不敏感在
	// launcherFilter 的入口统一处理。
	items := launcherFilter("CODE", testLauncherSources(), 9)
	found := false
	for _, it := range items {
		if it.title == "Visual Studio Code" {
			found = true
		}
	}
	if !found {
		t.Errorf("查询应大小写不敏感，结果：%+v", items)
	}

	// 命中辅助路径时降权，但仍应高于完全不匹配。辅助字段在生产路径里也已小写化。
	got := launcherScore("applications", "visual studio code", "applications/visual studio code.app")
	if got != 650 {
		t.Errorf("路径命中应得 800-150=650，实际 %d", got)
	}
	if launcherScore("zzz", "code", "/Applications") >= 0 {
		t.Error("完全不命中应返回 -1")
	}
}

func TestLauncherScoreMultiTerm(t *testing.T) {
	if got := launcherScoreTerms([]string{"visual", "code"}, "visual studio code", ""); got < 0 {
		t.Fatalf("两个词都命中时应返回正分，实际 %d", got)
	}
	if got := launcherScoreTerms([]string{"visual", "zzz"}, "visual studio code", ""); got >= 0 {
		t.Fatalf("任一词未命中应返回 -1，实际 %d", got)
	}
}

func testLauncherSources() launcherSources {
	return launcherSources{
		includeApps:  true,
		includeFiles: true,
		apps: []launcherAppEntry{
			{app: mygo.InstalledApp{Name: "Visual Studio Code", Path: "/Applications/Visual Studio Code.app"},
				nameLower: "visual studio code", pathLower: "/applications/visual studio code.app", altLower: "visual studio code"},
			{app: mygo.InstalledApp{Name: "Calendar", Path: "/System/Applications/Calendar.app"},
				nameLower: "calendar", pathLower: "/system/applications/calendar.app", altLower: "calendar"},
		},
		files: []launcherFileEntry{
			{path: "/Users/me/Documents/report.pdf", name: "report.pdf", dir: "/Users/me/Documents",
				nameLower: "report.pdf", pathLower: "/users/me/documents/report.pdf", depth: 1, rank: 2},
			{path: "/Users/me/Projects", name: "Projects", dir: "/Users/me",
				nameLower: "projects", pathLower: "/users/me/projects", isDir: true, depth: 1, rank: 3},
		},
	}
}

func TestLauncherFilterEmptyQueryNoResults(t *testing.T) {
	if items := launcherFilter("   ", testLauncherSources(), 9); len(items) != 0 {
		t.Fatalf("空查询不应返回结果，实际 %d 条：%+v", len(items), items)
	}
}

func TestLauncherFilterSources(t *testing.T) {
	src := testLauncherSources()
	src.includeApps = false
	items := launcherFilter("report", src, 9)
	if len(items) != 1 || items[0].kind != launcherKindFile {
		t.Fatalf("关闭应用源后应只剩文件结果：%+v", items)
	}

	src = testLauncherSources()
	src.includeFiles = false
	items = launcherFilter("report", src, 9)
	if len(items) != 0 {
		t.Errorf("关闭文件源后不应返回文件：%+v", items)
	}
}

func TestLauncherFilterOrderAndLimit(t *testing.T) {
	src := testLauncherSources()
	items := launcherFilter("c", src, 1)
	if len(items) != 1 {
		t.Fatalf("结果应被限制为 1 条，实际 %d", len(items))
	}
	if items[0].kind != launcherKindApp {
		t.Errorf("同分时应用应排在文件前，实际 %+v", items[0])
	}

	items = launcherFilter("report", src, 9)
	if len(items) != 1 || items[0].title != "report.pdf" {
		t.Fatalf("应匹配 report.pdf：%+v", items)
	}
}

func TestLauncherFilterDedupesSamePath(t *testing.T) {
	src := launcherSources{
		includeApps: true,
		apps: []launcherAppEntry{
			{app: mygo.InstalledApp{Name: "Code", Path: "/Applications/Code.app"},
				nameLower: "code", pathLower: "/applications/code.app"},
			{app: mygo.InstalledApp{Name: "Code Preview", Path: "/Applications/Code.app"},
				nameLower: "code preview", pathLower: "/applications/code.app"},
		},
	}
	items := launcherFilter("code", src, 9)
	if len(items) != 1 {
		t.Fatalf("同一路径应去重，实际 %d 条：%+v", len(items), items)
	}
}

func TestClampLauncherSelection(t *testing.T) {
	if got := clampLauncherSelection(0, 0, 1); got != 0 {
		t.Errorf("空列表应返回 0，实际 %d", got)
	}
	if got := clampLauncherSelection(0, 3, -1); got != 0 {
		t.Errorf("上越界应保持 0，实际 %d", got)
	}
	if got := clampLauncherSelection(2, 3, 1); got != 2 {
		t.Errorf("下越界应保持最后一项，实际 %d", got)
	}
	if got := clampLauncherSelection(1, 3, 1); got != 2 {
		t.Errorf("正常下移应为 2，实际 %d", got)
	}
}
