package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 浮窗在有结果时渲染原文、译文和错误提示。
func TestTranslationWindowRender(t *testing.T) {
	s := &translationSession{
		app:    newTestApp(),
		source: "hello world",
		target: "zh-CN",
		results: []translationResult{
			{ProviderID: "baidu", Name: "百度翻译", Kind: "baidu", Text: "你好世界"},
			{ProviderID: "google", Name: "Google 翻译", Kind: "google", Err: "请求超时，请重试"},
		},
	}
	tt := ui.NewTester(s.view, translationWidth, translationHeight)

	for _, want := range []string{"划词翻译", "hello world", "百度翻译", "你好世界", "请求超时，请重试"} {
		if !tt.HasText(want) {
			t.Errorf("浮窗应显示 %q，实际文本：%q", want, tt.Texts())
		}
	}
}

// 单个服务加载中时，卡片内显示等待提示。
func TestTranslationWindowLoading(t *testing.T) {
	a := newTestApp()
	a.providers = []Provider{
		{ID: "baidu", Name: "百度翻译", Kind: "baidu", Enabled: true, Configured: true},
	}
	s := &translationSession{
		app:    a,
		source: "hello",
		target: "zh-CN",
		results: []translationResult{
			{ProviderID: "baidu", Name: "百度翻译", Kind: "baidu", Loading: true},
		},
	}
	tt := ui.NewTester(s.view, translationWidth, translationHeight)
	if !tt.HasText("翻译中…") {
		t.Errorf("加载中应显示等待提示，实际文本：%q", tt.Texts())
	}
}

// 浮窗打开前失败的提示也要能看见（主窗口隐藏时 toast 看不到）。
func TestTranslationWindowNotice(t *testing.T) {
	s := &translationSession{
		app:    newTestApp(),
		notice: "读取选中文本失败：没有检测到选中的文本",
	}
	tt := ui.NewTester(s.view, translationWidth, 210)
	if !tt.HasText("没有检测到选中的文本") {
		t.Errorf("浮窗应显示失败原因，实际文本：%q", tt.Texts())
	}
}

// 没有可用服务时给出配置引导，而不是空白窗口。
func TestTranslationWindowEmpty(t *testing.T) {
	s := &translationSession{
		app:    newTestApp(),
		source: "hello",
		target: "zh-CN",
	}
	tt := ui.NewTester(s.view, translationWidth, translationHeight)
	if !tt.HasText("还没有可用的翻译服务") {
		t.Errorf("空结果应显示配置提示，实际文本：%q", tt.Texts())
	}
}
