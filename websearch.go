package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// 网页搜索预设。触发词选短、且不容易和项目名混淆的形式。
var webSearchPresets = []LauncherSearchEngine{
	{ID: "baidu", Name: "百度", Command: "bd", URL: "https://www.baidu.com/s?wd=%s"},
	{ID: "google", Name: "Google", Command: "gg", URL: "https://www.google.com/search?q=%s"},
	{ID: "bing", Name: "Bing", Command: "bing", URL: "https://www.bing.com/search?q=%s"},
}

// webSearchEngines 返回可以实际使用的引擎：开关打开、触发词非空且地址带 %s。
func (a *app) webSearchEngines() []LauncherSearchEngine {
	if a == nil || !a.settings.LauncherWebSearchEnabled {
		return nil
	}
	var out []LauncherSearchEngine
	for _, eng := range a.settings.LauncherWebSearchEngines {
		if strings.TrimSpace(eng.Command) == "" || !strings.Contains(eng.URL, "%s") {
			continue
		}
		out = append(out, eng)
	}
	return out
}

// webSearchName 返回引擎的显示名，没填名称时退回触发词。
func webSearchName(eng LauncherSearchEngine) string {
	if name := strings.TrimSpace(eng.Name); name != "" {
		return name
	}
	return strings.TrimSpace(eng.Command)
}

// webSearchQuery 判断输入是否是某个引擎的「触发词 + 空格」搜索，命令部分
// 允许只输入触发词的前缀。返回的空查询表示只输入了触发词。
func (s *launcherSession) webSearchQuery() (LauncherSearchEngine, string, bool) {
	if s == nil || s.app == nil {
		return LauncherSearchEngine{}, "", false
	}
	raw := strings.TrimLeft(s.query, " \t")
	sep := strings.IndexAny(raw, " \t")
	if sep <= 0 {
		return LauncherSearchEngine{}, "", false
	}
	command := raw[:sep]
	if utf8.RuneCountInString(command) < 2 {
		return LauncherSearchEngine{}, "", false
	}
	query := strings.TrimSpace(raw[sep+1:])
	for _, eng := range s.app.webSearchEngines() {
		prefix := strings.TrimSpace(eng.Command)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(prefix), strings.ToLower(command)) {
			return eng, query, true
		}
	}
	return LauncherSearchEngine{}, "", false
}

// buildWebSearchURL 把查询词填进地址模板。QueryEscape 会把空格转成 +，
// 百度、Google、Bing 都接受这种编码。
func buildWebSearchURL(template, query string) string {
	return strings.ReplaceAll(template, "%s", url.QueryEscape(query))
}

func openWebSearchURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return errors.New("搜索地址必须以 http:// 或 https:// 开头")
	}
	cmd := webSearchCommand(rawURL)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开浏览器失败：%w", err)
	}
	// 回收子进程，避免僵尸进程；启动器不等待浏览器退出。
	go func() { _ = cmd.Wait() }()
	return nil
}

// ---------------- 配置维护 ----------------

func (a *app) addWebSearchPreset(id string) {
	if a == nil {
		return
	}
	for _, eng := range a.settings.LauncherWebSearchEngines {
		if eng.ID == id {
			a.toast("该搜索引擎已添加")
			return
		}
	}
	var preset LauncherSearchEngine
	for _, p := range webSearchPresets {
		if p.ID == id {
			preset = p
			break
		}
	}
	if preset.ID == "" {
		return
	}
	if reason := a.webSearchCommandConflict(id, preset.Command); reason != "" {
		a.toast(reason)
		return
	}
	a.settings.LauncherWebSearchEngines = append(a.settings.LauncherWebSearchEngines, preset)
	a.afterWebSearchChange()
}

func (a *app) addCustomWebSearch() {
	if a == nil {
		return
	}
	engine := LauncherSearchEngine{
		ID:   fmt.Sprintf("custom-%d", time.Now().UnixNano()),
		Name: "自定义",
	}
	a.settings.LauncherWebSearchEngines = append(a.settings.LauncherWebSearchEngines, engine)
	a.afterWebSearchChange()
}

func (a *app) removeWebSearchEngine(id string) {
	if a == nil {
		return
	}
	engines := a.settings.LauncherWebSearchEngines[:0]
	for _, eng := range a.settings.LauncherWebSearchEngines {
		if eng.ID != id {
			engines = append(engines, eng)
		}
	}
	a.settings.LauncherWebSearchEngines = engines
	a.afterWebSearchChange()
}

// webSearchCommandConflict 检查搜索触发词是否与内置命令、快速打开触发词或
// 其他搜索引擎重名。空触发词不算冲突，表示还没配置完。
func (a *app) webSearchCommandConflict(engineID, command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if isBuiltinCommand(command) {
		return "“" + command + "”是内置命令，不能作为搜索触发词"
	}
	for _, reserved := range []string{a.vscodeCommand(), a.traeCommand(), a.forkCommand()} {
		if strings.EqualFold(command, reserved) {
			return "“" + command + "”已被快速打开触发词占用"
		}
	}
	for _, eng := range a.settings.LauncherWebSearchEngines {
		if eng.ID == engineID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(eng.Command), command) {
			return "“" + command + "”已被其他搜索引擎占用"
		}
	}
	return ""
}

// projectCommandConflict 检查项目触发词是否与内置命令或搜索触发词重名。
func (a *app) projectCommandConflict(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if isBuiltinCommand(command) {
		return "“" + command + "”是内置命令，不能作为触发词"
	}
	for _, eng := range a.settings.LauncherWebSearchEngines {
		if strings.EqualFold(strings.TrimSpace(eng.Command), command) {
			return "“" + command + "”已被网页搜索触发词占用"
		}
	}
	return ""
}

// webSearchPrefix 返回某个搜索引擎的触发词，找不到时为空。
func (a *app) webSearchPrefix(id string) string {
	if a == nil {
		return ""
	}
	for _, eng := range a.settings.LauncherWebSearchEngines {
		if eng.ID == id {
			return strings.TrimSpace(eng.Command)
		}
	}
	return ""
}

// afterWebSearchChange 保存引擎改动并刷新已打开的浮窗。
func (a *app) afterWebSearchChange() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.launcher != nil {
		a.launcher.sourcesChanged()
	}
}
