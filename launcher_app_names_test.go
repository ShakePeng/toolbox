package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func TestPlistLocalizedStringTextFormat(t *testing.T) {
	data := []byte(`/* 中文名 */
"CFBundleDisplayName" = "同程管家";
"CFBundleName" = "TIEXIN";
`)
	if got := plistLocalizedString(data, "CFBundleDisplayName"); got != "同程管家" {
		t.Errorf("应解析出中文显示名，实际 %q", got)
	}
	if got := plistLocalizedString(data, "CFBundleName"); got != "TIEXIN" {
		t.Errorf("应解析出英文名，实际 %q", got)
	}
	if got := plistLocalizedString(data, "CFBundleIdentifier"); got != "" {
		t.Errorf("不存在的键应返回空，实际 %q", got)
	}
}

func TestPlistLocalizedStringXMLFormat(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleDisplayName</key>
<string>同程管家</string>
</dict></plist>`)
	if got := plistLocalizedString(data, "CFBundleDisplayName"); got != "同程管家" {
		t.Errorf("XML 格式应解析出中文名，实际 %q", got)
	}
}

// 微信等应用的 InfoPlist.strings 是带 BOM 的 UTF-16，需要先转码才能解析。
func TestPlistLocalizedStringUTF16(t *testing.T) {
	src := `"CFBundleDisplayName" = "微信";` + "\n"
	encoded, _, err := transform.Bytes(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := plistLocalizedString(encoded, "CFBundleDisplayName"); got != "微信" {
		t.Errorf("UTF-16 字符串文件应解析出中文名，实际 %q", got)
	}
}

// 二进制 plist 不手写解析，返回空串让调用方退回系统原名。
func TestPlistLocalizedStringSkipsBinary(t *testing.T) {
	if got := plistLocalizedString([]byte("bplist00\x00\x01"), "CFBundleDisplayName"); got != "" {
		t.Errorf("二进制 plist 应跳过，实际 %q", got)
	}
}

func TestLocalizedAppName(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "TIEXIN.app")
	lproj := filepath.Join(app, "Contents", "Resources", "zh-Hans.lproj")
	if err := os.MkdirAll(lproj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lproj, "InfoPlist.strings"),
		[]byte(`"CFBundleDisplayName" = "同程管家";`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := localizedAppName(app); got != "同程管家" {
		t.Errorf("应读取中文本地化名，实际 %q", got)
	}
	if got := localizedAppName(filepath.Join(root, "Missing.app")); got != "" {
		t.Errorf("不存在的应用应返回空，实际 %q", got)
	}
}

// 系统给的是英文名时，启动器用中文名展示，同时保留英文原名可被搜索。
// 系统自带应用没有 InfoPlist.strings，名字来自 Spotlight，英文原名仍可搜索。
func TestBuildLauncherAppEntriesPrefersSpotlight(t *testing.T) {
	orig := lookupSpotlightNames
	lookupSpotlightNames = func(paths []string) map[string]string {
		return map[string]string{paths[0]: "终端"}
	}
	defer func() { lookupSpotlightNames = orig }()

	entries := buildLauncherAppEntries([]mygo.InstalledApp{
		{Name: "Terminal", ID: "com.apple.Terminal", Path: "/System/Applications/Utilities/Terminal.app"},
	})
	if len(entries) != 1 {
		t.Fatalf("应生成一个应用条目，实际 %d", len(entries))
	}
	e := entries[0]
	if e.app.Name != "终端" {
		t.Errorf("显示名应取 Spotlight 的中文名，实际 %q", e.app.Name)
	}
	if e.nameLower != "终端" {
		t.Errorf("中文名应参与名称匹配，实际 %q", e.nameLower)
	}
	if !strings.Contains(e.altLower, "terminal") {
		t.Errorf("英文原名应保留为搜索别名，实际 %q", e.altLower)
	}
}

func TestBuildLauncherAppEntriesUsesLocalizedName(t *testing.T) {
	// 测试路径不在 Spotlight 索引里，用注入避免真实调用 mdls。
	orig := lookupSpotlightNames
	lookupSpotlightNames = func([]string) map[string]string { return nil }
	defer func() { lookupSpotlightNames = orig }()

	root := t.TempDir()
	app := filepath.Join(root, "TIEXIN.app")
	lproj := filepath.Join(app, "Contents", "Resources", "zh-Hans.lproj")
	if err := os.MkdirAll(lproj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lproj, "InfoPlist.strings"),
		[]byte(`"CFBundleDisplayName" = "同程管家";`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := buildLauncherAppEntries([]mygo.InstalledApp{
		{Name: "TIEXIN", ID: "com.tongcheng.tcmanager", Path: app},
	})
	if len(entries) != 1 {
		t.Fatalf("应生成一个应用条目，实际 %d", len(entries))
	}
	e := entries[0]
	if e.app.Name != "同程管家" {
		t.Errorf("显示名应为中文名，实际 %q", e.app.Name)
	}
	if e.app.Path != app || e.app.ID != "com.tongcheng.tcmanager" {
		t.Errorf("路径与标识应保持不变：%+v", e.app)
	}
	if e.nameLower != "同程管家" {
		t.Errorf("中文名应参与名称匹配，实际 %q", e.nameLower)
	}
	for _, alias := range []string{"tiexin", "com.tongcheng.tcmanager"} {
		if !strings.Contains(e.altLower, alias) {
			t.Errorf("搜索别名应包含 %q，实际 %q", alias, e.altLower)
		}
	}
}
