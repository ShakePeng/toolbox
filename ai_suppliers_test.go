package main

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAISupplierDraftModels(t *testing.T) {
	d := &aiSupplierDraft{Selected: map[string]bool{}}
	d.mergeModels([]string{"gpt-4o", " gpt-4o ", "GPT-4O", "gpt-4o-mini"})
	if !slices.Equal(d.Available, []string{"gpt-4o", "gpt-4o-mini"}) {
		t.Fatalf("模型应去空行并按大小写去重排序，实际 %v", d.Available)
	}
	if len(d.selectedModels()) != 2 {
		t.Errorf("获取到的模型应默认勾选，实际 %v", d.selectedModels())
	}

	d.Selected["gpt-4o"] = false
	if !slices.Equal(d.selectedModels(), []string{"gpt-4o-mini"}) {
		t.Errorf("取消勾选后不应再保存该模型，实际 %v", d.selectedModels())
	}

	d.addModel("deepseek-v3")
	if !d.Selected["deepseek-v3"] || !slices.Contains(d.Available, "deepseek-v3") {
		t.Errorf("手动添加的模型应进入列表并勾选：%v", d.Available)
	}
	d.addModel("")
	if len(d.Available) != 3 {
		t.Errorf("空模型不应添加，实际 %v", d.Available)
	}
}

func TestSaveAISupplier(t *testing.T) {
	a := newTestApp()
	// save 会 persist，指向临时文件避免写用户配置。
	a.configPath = filepath.Join(t.TempDir(), "config.json")

	d := &aiSupplierDraft{idx: -1, Enabled: true, Selected: map[string]bool{}}
	d.Name = " 中转站 "
	d.BaseURL = "https://api.example.com/v1"
	d.APIKey = " sk-x "
	d.mergeModels([]string{"m1", "m2"})
	a.aiDraft = d
	a.saveAISupplier()
	if len(a.settings.AISuppliers) != 1 {
		t.Fatalf("应保存一个供应商，实际 %+v", a.settings.AISuppliers)
	}
	s := a.settings.AISuppliers[0]
	if s.Name != "中转站" || s.APIKey != "sk-x" || s.BaseURL != "https://api.example.com/v1" {
		t.Errorf("字段应去掉首尾空格后保存：%+v", s)
	}
	if !slices.Equal(s.Models, []string{"m1", "m2"}) {
		t.Errorf("应保存勾选的模型，实际 %v", s.Models)
	}
	if s.ID == "" || !s.Enabled {
		t.Errorf("应生成 ID 并保持启用：%+v", s)
	}

	// 编辑已有供应商不应新增。
	a.aiDraft = newAISupplierDraft(0, s)
	a.aiDraft.Name = "改名"
	a.saveAISupplier()
	if len(a.settings.AISuppliers) != 1 || a.settings.AISuppliers[0].Name != "改名" {
		t.Fatalf("编辑后应原地更新，实际 %+v", a.settings.AISuppliers)
	}

	// 名称为空或地址协议不对时不保存。
	a.aiDraft = &aiSupplierDraft{idx: -1, Name: "", BaseURL: "https://x", Selected: map[string]bool{}}
	a.saveAISupplier()
	a.aiDraft = &aiSupplierDraft{idx: -1, Name: "x", BaseURL: "ftp://x", Selected: map[string]bool{}}
	a.saveAISupplier()
	if len(a.settings.AISuppliers) != 1 {
		t.Errorf("非法配置不应保存，实际 %+v", a.settings.AISuppliers)
	}
}

func TestFetchAISupplierModels(t *testing.T) {
	old := fetchAISupplierModels
	defer func() { fetchAISupplierModels = old }()
	fetchAISupplierModels = func(baseURL, apiKey string) ([]string, error) {
		if baseURL != "https://api.example.com/v1" || apiKey != "sk-1" {
			t.Errorf("请求参数不对：%q %q", baseURL, apiKey)
		}
		return []string{"m2", "m1"}, nil
	}

	a := newTestApp()
	a.aiDraft = &aiSupplierDraft{
		idx: -1, BaseURL: "https://api.example.com/v1", APIKey: "sk-1",
		Selected: map[string]bool{},
	}
	a.startAISupplierModelFetch()
	if a.aiDraft.Fetching {
		t.Error("无窗口时应同步完成获取")
	}
	if !slices.Equal(a.aiDraft.Available, []string{"m1", "m2"}) {
		t.Errorf("应合并并排序模型，实际 %v", a.aiDraft.Available)
	}
	if a.aiDraft.FetchErr != "" {
		t.Errorf("获取成功不应有错误：%s", a.aiDraft.FetchErr)
	}
}

func TestFetchAISupplierModelsError(t *testing.T) {
	old := fetchAISupplierModels
	defer func() { fetchAISupplierModels = old }()
	fetchAISupplierModels = func(string, string) ([]string, error) {
		return nil, errors.New("服务不可用")
	}

	a := newTestApp()
	a.aiDraft = &aiSupplierDraft{idx: -1, BaseURL: "https://x", Selected: map[string]bool{}}
	a.startAISupplierModelFetch()
	if a.aiDraft.FetchErr != "服务不可用" {
		t.Errorf("获取失败应显示错误，实际 %q", a.aiDraft.FetchErr)
	}
}

func TestRemoveAISupplier(t *testing.T) {
	a := newTestApp()
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	a.settings.AISuppliers = []AISupplier{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
	}
	a.removeAISupplier(0)
	if len(a.settings.AISuppliers) != 1 || a.settings.AISuppliers[0].ID != "b" {
		t.Fatalf("应删除第一个供应商，实际 %+v", a.settings.AISuppliers)
	}
}

func TestAISupplierPageRender(t *testing.T) {
	a := newTestApp()
	a.section = "ai"
	a.settings.AISuppliers = []AISupplier{{
		ID: "ai-1", Name: "OpenAI", BaseURL: "https://api.openai.com/v1",
		Models: []string{"gpt-4o"}, Enabled: true,
	}}
	tt := ui.NewTester(a.view, 1080, 800)
	tt.Frame()
	for _, text := range []string{"AI供应商", "OpenAI", "1 个模型", "添加供应商"} {
		if !tt.HasText(text) {
			t.Errorf("页面应显示 %q，实际文本：%q", text, tt.Texts())
		}
	}
}

func TestAISupplierModalModelPicker(t *testing.T) {
	a := newTestApp()
	a.section = "ai"
	a.aiDraft = newAISupplierDraft(-1, AISupplier{})
	a.aiDraft.mergeModels([]string{"gpt-4o", "gpt-4o-mini"})
	tt := ui.NewTester(a.view, 1080, 900)
	tt.Frame()
	for _, text := range []string{"获取模型", "gpt-4o", "已选 2 / 2"} {
		if !tt.HasText(text) {
			t.Errorf("弹窗应显示 %q，实际文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("手动添加模型") {
		t.Error("获取成功时不应显示手动添加入口")
	}
	if tt.HasText("默认模型") {
		t.Error("默认模型字段已经移除")
	}

	// 获取失败后才放开手动添加。
	a.aiDraft.FetchErr = "服务不可用"
	tt.Frame()
	if !tt.HasText("手动添加模型") {
		t.Errorf("获取失败时应显示手动添加入口，实际文本：%q", tt.Texts())
	}
}

// 模型胶囊点击后应切换勾选状态。
func TestAISupplierModelChipToggle(t *testing.T) {
	a := newTestApp()
	a.section = "ai"
	a.aiDraft = newAISupplierDraft(-1, AISupplier{})
	a.aiDraft.mergeModels([]string{"gpt-4o", "gpt-4o-mini"})
	tt := ui.NewTester(a.view, 1080, 900)
	tt.Frame()
	if err := tt.Click("选择模型 gpt-4o"); err != nil {
		t.Fatalf("点击模型胶囊失败：%v", err)
	}
	tt.Frame()
	if a.aiDraft.Selected["gpt-4o"] {
		t.Error("再次点击应取消勾选")
	}
	if !a.aiDraft.Selected["gpt-4o-mini"] {
		t.Error("其他模型不应受影响")
	}
}

func TestAISupplierNavAlwaysVisible(t *testing.T) {
	a := newTestApp()
	a.settings.TranslationEnabled = false
	a.settings.ScreenshotEnabled = false
	a.settings.LauncherEnabled = false
	tt := ui.NewTester(a.view, 1080, 800)
	tt.Frame()
	if !tt.HasText("AI供应商") {
		t.Fatalf("侧栏应始终显示 AI供应商，实际文本：%q", tt.Texts())
	}
}
