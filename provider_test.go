package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// AI 翻译的地址和密钥应从所选 AI 供应商解析。
func TestResolveProviderUsesAISupplier(t *testing.T) {
	a := newTestApp()
	a.settings.AISuppliers = []AISupplier{{
		ID: "ai-1", Name: "workbuddy", BaseURL: "https://api.example.com/v1",
		APIKey: "sk-1", Enabled: true, Models: []string{"m1"},
	}}
	p := a.resolveProvider(Provider{Kind: "openai-compatible", SupplierID: "ai-1", Model: "m1", Configured: true})
	if p.BaseURL != "https://api.example.com/v1" || p.Credentials["apiKey"] != "sk-1" {
		t.Fatalf("应从供应商填充地址和密钥：%+v", p)
	}

	a.settings.AISuppliers[0].Enabled = false
	if a.resolveProvider(Provider{Kind: "openai-compatible", SupplierID: "ai-1", Model: "m1", Configured: true}).Configured {
		t.Error("停用的供应商不应可用")
	}

	a.settings.AISuppliers = nil
	if a.resolveProvider(Provider{Kind: "openai-compatible", SupplierID: "ai-1", Model: "m1", Configured: true}).Configured {
		t.Error("供应商被删除后不应可用")
	}
}

// 打开 AI 翻译配置时自动选中第一个启用供应商和它的第一个模型。
func TestEnsureDraftSupplier(t *testing.T) {
	a := newTestApp()
	a.settings.AISuppliers = []AISupplier{{
		ID: "ai-1", Name: "A", Enabled: true, Models: []string{"m1", "m2"},
	}}
	d := newDraft(2, a.providers[2])
	a.ensureDraftSupplier(d)
	if d.SupplierID != "ai-1" || d.Model != "m1" {
		t.Fatalf("应默认选中第一个供应商和模型，实际 %+v", d)
	}

	// 已有合法选择时保持不变。
	d = &providerDraft{SupplierID: "ai-1", Model: "m2"}
	a.ensureDraftSupplier(d)
	if d.Model != "m2" {
		t.Errorf("合法模型不应被改写，实际 %q", d.Model)
	}
}

func TestAITranslationModal(t *testing.T) {
	a := newTestApp()
	a.settings.TranslationEnabled = true
	a.section = "translation"
	a.settings.AISuppliers = []AISupplier{{
		ID: "ai-1", Name: "workbuddy", BaseURL: "https://x/v1",
		Enabled: true, Models: []string{"m1", "m2"},
	}}
	a.openProvider(2)

	tt := ui.NewTester(a.view, 1080, 800)
	tt.Frame()
	for _, text := range []string{"AI 翻译", "供应商", "workbuddy", "模型"} {
		if !tt.HasText(text) {
			t.Errorf("弹窗应显示 %q，实际文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("API Key") || tt.HasText("Base URL") {
		t.Errorf("AI 翻译不应再手填地址和密钥，实际文本：%q", tt.Texts())
	}
}

func TestAITranslationModalWithoutSuppliers(t *testing.T) {
	a := newTestApp()
	a.settings.TranslationEnabled = true
	a.section = "translation"
	a.openProvider(2)

	tt := ui.NewTester(a.view, 1080, 800)
	tt.Frame()
	if !tt.HasText("还没有启用的 AI 供应商") {
		t.Fatalf("没有供应商时应显示引导，实际文本：%q", tt.Texts())
	}
}
