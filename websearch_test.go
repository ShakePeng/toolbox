package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWebSearchURL(t *testing.T) {
	if got := buildWebSearchURL("https://www.baidu.com/s?wd=%s", "hello world"); got != "https://www.baidu.com/s?wd=hello+world" {
		t.Errorf("英文关键词编码错误：%q", got)
	}
	if got := buildWebSearchURL("https://www.baidu.com/s?wd=%s", "天气"); got != "https://www.baidu.com/s?wd=%E5%A4%A9%E6%B0%94" {
		t.Errorf("中文关键词应做 URL 编码，实际 %q", got)
	}
}

func TestWebSearchQuery(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	s.app.settings.LauncherWebSearchEnabled = true
	s.app.settings.LauncherWebSearchEngines = []LauncherSearchEngine{
		{ID: "baidu", Name: "百度", Command: "bd", URL: "https://www.baidu.com/s?wd=%s"},
	}

	cases := []struct {
		query string
		want  string
		ok    bool
	}{
		{"bd 天气", "天气", true},
		{"BD 天气", "天气", true},
		{"b 天气", "", false},
		{"bd", "", false},
		{"bd ", "", true},
		{"bx 天气", "", false},
	}
	for _, c := range cases {
		s.query = c.query
		_, got, ok := s.webSearchQuery()
		if got != c.want || ok != c.ok {
			t.Errorf("query=%q → (%q,%v)，期望 (%q,%v)", c.query, got, ok, c.want, c.ok)
		}
	}
}

func TestWebSearchLauncherOpensURL(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	s.app.settings.LauncherWebSearchEnabled = true
	s.app.settings.LauncherWebSearchEngines = []LauncherSearchEngine{
		{ID: "baidu", Name: "百度", Command: "bd", URL: "https://www.baidu.com/s?wd=%s"},
	}
	opened := ""
	s.openSearch = func(u string) error {
		opened = u
		return nil
	}

	s.query = "bd 天气"
	s.rebuildResults()
	if len(s.results) != 1 || s.results[0].kind != launcherKindSearch {
		t.Fatalf("应只显示一条搜索入口，实际 %+v", s.results)
	}
	s.activate(0)
	if opened != "https://www.baidu.com/s?wd=%E5%A4%A9%E6%B0%94" {
		t.Fatalf("应打开编码后的搜索地址，实际 %q", opened)
	}
}

// 只输入触发词时先给提示行，回车补全成「触发词 + 空格」。
func TestWebSearchPrefixCompletes(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	s.app.settings.LauncherWebSearchEnabled = true
	s.app.settings.LauncherWebSearchEngines = []LauncherSearchEngine{
		{ID: "baidu", Name: "百度", Command: "bd", URL: "https://www.baidu.com/s?wd=%s"},
	}

	s.query = "b"
	s.rebuildResults()
	for _, item := range s.results {
		if item.action == launcherActionWebSearch {
			t.Fatalf("单字符触发词不应提示搜索：%+v", item)
		}
	}

	s.query = "bd"
	s.rebuildResults()
	if len(s.results) == 0 || s.results[0].action != launcherActionWebSearch {
		t.Fatalf("输入完整触发词应提示搜索，实际 %+v", s.results)
	}
	s.activate(0)
	if s.query != "bd " {
		t.Fatalf("回车后应补全为完整触发词，实际 %q", s.query)
	}
}

func TestWebSearchCommandConflict(t *testing.T) {
	a := newTestApp()
	a.settings.LauncherWebSearchEngines = []LauncherSearchEngine{
		{ID: "baidu", Command: "bd"},
	}
	if a.webSearchCommandConflict("google", "reindex") == "" {
		t.Error("内置命令应判定为冲突")
	}
	if a.webSearchCommandConflict("google", "code") == "" {
		t.Error("快速打开触发词应判定为冲突")
	}
	if a.webSearchCommandConflict("google", "bd") == "" {
		t.Error("其他搜索引擎的触发词应判定为冲突")
	}
	if a.webSearchCommandConflict("baidu", "bd") != "" {
		t.Error("引擎自身的触发词不算冲突")
	}
	if a.webSearchCommandConflict("google", "zz") != "" {
		t.Error("未占用的触发词不应报冲突")
	}
}

func TestWebSearchPresetsAndRemove(t *testing.T) {
	a := newTestApp()
	// add/remove 会 persist，指向临时文件避免写用户配置。
	a.configPath = filepath.Join(t.TempDir(), "config.json")

	a.addWebSearchPreset("baidu")
	a.addWebSearchPreset("baidu")
	a.addWebSearchPreset("google")
	if len(a.settings.LauncherWebSearchEngines) != 2 {
		t.Fatalf("应添加两个预设，实际 %+v", a.settings.LauncherWebSearchEngines)
	}
	if !strings.Contains(a.settings.LauncherWebSearchEngines[0].URL, "%s") {
		t.Errorf("预设地址应带关键词占位符：%+v", a.settings.LauncherWebSearchEngines[0])
	}

	a.removeWebSearchEngine("baidu")
	if len(a.settings.LauncherWebSearchEngines) != 1 || a.settings.LauncherWebSearchEngines[0].ID != "google" {
		t.Fatalf("删除后应只剩 Google，实际 %+v", a.settings.LauncherWebSearchEngines)
	}
}

// 地址缺少 %s 的引擎不参与匹配，避免打开一个没有关键词的地址。
func TestWebSearchRequiresPlaceholder(t *testing.T) {
	a := newTestApp()
	a.settings.LauncherWebSearchEnabled = true
	a.settings.LauncherWebSearchEngines = []LauncherSearchEngine{
		{ID: "broken", Name: "坏地址", Command: "xx", URL: "https://example.com/search"},
	}
	if len(a.webSearchEngines()) != 0 {
		t.Fatalf("缺少占位符的引擎不应生效：%+v", a.webSearchEngines())
	}
}
