package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 目标语言：自动模式按原文是否含中文决定。
func TestResolveTargetLang(t *testing.T) {
	cases := []struct {
		pref, text, want string
	}{
		{"auto", "hello world", "zh-CN"},
		{"auto", "你好，世界", "en"},
		{"auto", "これはテスト", "zh-CN"},
		// 日文通常含汉字，不能因此判成中文；韩文同理。
		{"auto", "こんにちは世界", "zh-CN"},
		{"auto", "안녕하세요 世界", "zh-CN"},
		{"auto", "混合中文 and English", "en"},
		{"ja", "hello", "ja"},
		{"zh-TW", "hello", "zh-TW"},
	}
	for _, c := range cases {
		if got := resolveTargetLang(c.pref, c.text); got != c.want {
			t.Errorf("resolveTargetLang(%q, %q) = %q，期望 %q", c.pref, c.text, got, c.want)
		}
	}
}

// 百度与 OpenAI 的语言/地址映射。
func TestProviderMappings(t *testing.T) {
	langs := map[string]string{
		"zh-CN": "zh",
		"zh-TW": "cht",
		"en":    "en",
		"ja":    "jp",
		"ko":    "kor",
	}
	for in, want := range langs {
		if got := baiduLang(in); got != want {
			t.Errorf("baiduLang(%q) = %q，期望 %q", in, got, want)
		}
	}

	urls := map[string]string{
		"http://localhost:8000/v1":                  "http://localhost:8000/v1/chat/completions",
		"http://localhost:8000/v1/":                 "http://localhost:8000/v1/chat/completions",
		"http://localhost:8000/v1/chat/completions": "http://localhost:8000/v1/chat/completions",
		"": "",
	}
	for in, want := range urls {
		if got := chatCompletionsURL(in); got != want {
			t.Errorf("chatCompletionsURL(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 三个服务都能解析出译文，并且结果顺序与配置一致。
func TestTranslateAllProviders(t *testing.T) {
	baidu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		salt := q.Get("salt")
		sum := md5.Sum([]byte(q.Get("appid") + q.Get("q") + salt + "secret"))
		if sign := q.Get("sign"); sign != hex.EncodeToString(sum[:]) {
			writeJSON(t, w, map[string]any{"error_code": "54001", "error_msg": "INVALID SIGN"})
			return
		}
		writeJSON(t, w, map[string]any{
			"from": "en",
			"to":   "zh",
			"trans_result": []map[string]string{
				{"src": "hello", "dst": "你好"},
				{"src": "world", "dst": "世界"},
			},
		})
	}))
	defer baidu.Close()

	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "google-key" {
			t.Errorf("Google key = %q", got)
		}
		var body struct {
			Q      string `json:"q"`
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析 Google 请求失败：%v", err)
		}
		if body.Target != "zh-CN" {
			t.Errorf("Google target = %q，期望 zh-CN", body.Target)
		}
		writeJSON(t, w, map[string]any{
			"data": map[string]any{
				"translations": []map[string]string{{"translatedText": "你好 &amp; 世界"}},
			},
		})
	}))
	defer google.Close()

	openai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/chat/completions" {
			t.Errorf("OpenAI path = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer openai-key" {
			t.Errorf("OpenAI Authorization = %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("解析 OpenAI 请求失败：%v", err)
		}
		if body.Model != "test-model" {
			t.Errorf("OpenAI model = %q", body.Model)
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, "Simplified Chinese") {
			t.Errorf("OpenAI 提示词未指定目标语言：%+v", body.Messages)
		}
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": " 你好世界 "}},
			},
		})
	}))
	defer openai.Close()

	oldBaidu, oldGoogle := baiduEndpoint, googleEndpoint
	baiduEndpoint = baidu.URL
	googleEndpoint = google.URL
	defer func() { baiduEndpoint, googleEndpoint = oldBaidu, oldGoogle }()

	providers := []Provider{
		{ID: "baidu", Name: "百度翻译", Kind: "baidu", Enabled: true, Configured: true,
			Credentials: map[string]string{"appId": "appid", "secret": "secret"}},
		{ID: "google", Name: "Google 翻译", Kind: "google", Enabled: true, Configured: true,
			Credentials: map[string]string{"apiKey": "google-key"}},
		{ID: "openai", Name: "OpenAI", Kind: "openai-compatible", Enabled: true, Configured: true,
			BaseURL: openai.URL + "/v1", Model: "test-model",
			Credentials: map[string]string{"apiKey": "openai-key"}},
		// 未启用/未配置的服务不参与翻译。
		{ID: "off", Name: "未启用", Kind: "google", Enabled: false, Configured: true,
			Credentials: map[string]string{"apiKey": "x"}},
	}

	got := translateAll("hello world", "zh-CN", providers)
	if len(got) != 3 {
		t.Fatalf("结果数量 = %d，期望 3：%+v", len(got), got)
	}
	want := []struct {
		id, text string
	}{
		{"baidu", "你好世界"},
		{"google", "你好 & 世界"},
		{"openai", "你好世界"},
	}
	for i, w := range want {
		if got[i].ProviderID != w.id {
			t.Errorf("第 %d 个结果来自 %q，期望 %q", i, got[i].ProviderID, w.id)
		}
		if got[i].Err != "" {
			t.Errorf("%s 翻译失败：%s", got[i].ProviderID, got[i].Err)
		}
		if got[i].Text != w.text {
			t.Errorf("%s 译文 = %q，期望 %q", got[i].ProviderID, got[i].Text, w.text)
		}
	}
}

// 服务返回错误码时，结果里应带上可读的中文提示，而不是整段 JSON。
func TestTranslateBaiduError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"error_code": "54001", "error_msg": "INVALID SIGN"})
	}))
	defer srv.Close()

	old := baiduEndpoint
	baiduEndpoint = srv.URL
	defer func() { baiduEndpoint = old }()

	_, err := translateBaidu(t.Context(), srv.Client(), Provider{
		Kind:        "baidu",
		Credentials: map[string]string{"appId": "a", "secret": "s"},
	}, "hello", "zh-CN")
	if err == nil || !strings.Contains(err.Error(), "签名错误") {
		t.Fatalf("期望签名错误提示，实际：%v", err)
	}
}

func TestTranslateProvidersFilter(t *testing.T) {
	ps := []Provider{
		{ID: "a", Enabled: true, Configured: true},
		{ID: "b", Enabled: true, Configured: false},
		{ID: "c", Enabled: false, Configured: true},
	}
	if got := translationProviders(ps); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("过滤结果错误：%+v", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("写响应失败：%v", err)
	}
}
