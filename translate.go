package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/egoist/mygo/netproxy"
)

// 翻译请求的最长等待时间。划词场景要的是快，超时后给出明确错误比一直转圈好。
const translateTimeout = 15 * time.Second

// 各服务的接口地址放在变量里，测试可以替换成 httptest 的本地服务。
var (
	baiduEndpoint  = "https://fanyi-api.baidu.com/api/trans/vip/translate"
	googleEndpoint = "https://translation.googleapis.com/language/translate/v2"
)

// translationResult 是一次翻译里某个服务的结果。Err 用字符串保存，
// 方便直接展示到界面；空字符串表示成功。
type translationResult struct {
	ProviderID string
	Name       string
	Kind       string
	Model      string
	Text       string
	Detected   string
	ElapsedMS  int64
	// Loading 只用于浮窗的逐卡片加载态，翻译完成后清零。
	Loading bool
	Err     string
}

// providerOutput 是单个服务返回的译文和它检测到的源语言。
type providerOutput struct {
	Text     string
	Detected string
}

// aiSupplierByID 返回指定 AI 供应商。
func (a *app) aiSupplierByID(id string) (AISupplier, bool) {
	if a == nil || strings.TrimSpace(id) == "" {
		return AISupplier{}, false
	}
	for _, supplier := range a.settings.AISuppliers {
		if supplier.ID == id {
			return supplier, true
		}
	}
	return AISupplier{}, false
}

// resolveProvider 把 AI 翻译引用的供应商地址和密钥填进 Provider 副本。
// 供应商被删除或停用时标记为未配置，翻译列表就不再显示它。
func (a *app) resolveProvider(p Provider) Provider {
	if p.Kind != "openai-compatible" {
		return p
	}
	supplier, ok := a.aiSupplierByID(p.SupplierID)
	if !ok || !supplier.Enabled {
		p.Configured = false
		return p
	}
	p.BaseURL = supplier.BaseURL
	p.Credentials = map[string]string{"apiKey": supplier.APIKey}
	if strings.TrimSpace(p.Model) == "" && len(supplier.Models) > 0 {
		p.Model = supplier.Models[0]
	}
	if strings.TrimSpace(p.Model) == "" {
		p.Configured = false
	}
	return p
}

// resolvedProviders 返回填好供应商凭据的 Provider 副本，翻译请求统一用它。
func (a *app) resolvedProviders() []Provider {
	out := make([]Provider, len(a.providers))
	for i, p := range a.providers {
		out[i] = a.resolveProvider(p)
	}
	return out
}

// translationProviders 返回当前已启用且配置完整的服务，顺序与配置一致。
func translationProviders(providers []Provider) []Provider {
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if p.Enabled && p.Configured {
			out = append(out, p)
		}
	}
	return out
}

// translateAll 并发请求所有可用服务，结果顺序与传入顺序一致。
// 单个服务失败不影响其他服务。
func translateAll(text, target string, providers []Provider) []translationResult {
	usable := translationProviders(providers)
	if len(usable) == 0 {
		return nil
	}

	results := make([]translationResult, len(usable))
	var wg sync.WaitGroup
	for i, p := range usable {
		results[i] = translationResult{ProviderID: p.ID, Name: p.Name, Kind: p.Kind, Model: p.Model}
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			results[i] = translateOne(p, text, target)
		}(i, p)
	}
	wg.Wait()
	return results
}

// translateOne 请求单个服务并记录耗时。浮窗和“保存并测试”都走这里。
func translateOne(p Provider, text, target string) translationResult {
	r := translationResult{ProviderID: p.ID, Name: p.Name, Kind: p.Kind, Model: p.Model}
	client := newHTTPClient(p.UseSystemProxy)
	defer client.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), translateTimeout)
	defer cancel()

	start := time.Now()
	out, err := translateWith(ctx, client, p, text, target)
	r.ElapsedMS = time.Since(start).Milliseconds()
	if err != nil {
		r.Err = err.Error()
		return r
	}
	r.Text = out.Text
	r.Detected = out.Detected
	return r
}

// testProvider 在后台向服务发一次真实翻译请求，用于“保存并测试”。
// 结果通过主线程 toast 告知，不阻塞配置弹窗。
func (a *app) testProvider(idx int) {
	if idx < 0 || idx >= len(a.providers) {
		return
	}
	p := a.resolveProvider(a.providers[idx])
	a.toast(p.Name + "：正在测试连接…")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), translateTimeout)
		defer cancel()
		client := newHTTPClient(p.UseSystemProxy)
		defer client.CloseIdleConnections()

		// 用一段英文探测，自动目标语言会把它译成中文。
		_, err := translateWith(ctx, client, p, "hello", resolveTargetLang("auto", "hello"))

		if a.win == nil {
			return
		}
		a.win.Update(func() {
			if err != nil {
				a.toast(p.Name + "：测试失败 - " + err.Error())
				return
			}
			a.toast(p.Name + "：连接正常，翻译可用")
		})
	}()
}

// newHTTPClient 创建翻译用的客户端。走系统代理时用 netproxy，
// 直连时显式关闭代理，避免环境变量里的代理意外生效。
func newHTTPClient(useSystemProxy bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	if useSystemProxy {
		tr.Proxy = netproxy.Proxy
	}
	return &http.Client{Transport: tr}
}

func translateWith(ctx context.Context, client *http.Client, p Provider, text, target string) (providerOutput, error) {
	switch p.Kind {
	case "baidu":
		return translateBaidu(ctx, client, p, text, target)
	case "google":
		return translateGoogle(ctx, client, p, text, target)
	case "openai-compatible":
		return translateOpenAI(ctx, client, p, text, target)
	}
	return providerOutput{}, fmt.Errorf("暂不支持的服务类型：%s", p.Kind)
}

// ---------------- 百度 ----------------

type baiduResponse struct {
	From        string `json:"from"`
	To          string `json:"to"`
	TransResult []struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	} `json:"trans_result"`
	ErrorCode string `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

func translateBaidu(ctx context.Context, client *http.Client, p Provider, text, target string) (providerOutput, error) {
	appID := p.Credentials["appId"]
	secret := p.Credentials["secret"]
	if appID == "" || secret == "" {
		return providerOutput{}, errors.New("缺少 App ID 或 App 密钥")
	}
	// 百度要求 sign = md5(appid + q + salt + secret)，salt 随机即可。
	salt := strconv.FormatInt(time.Now().UnixNano(), 10)
	sum := md5.Sum([]byte(appID + text + salt + secret))

	q := url.Values{}
	q.Set("q", text)
	q.Set("from", "auto")
	q.Set("to", baiduLang(target))
	q.Set("appid", appID)
	q.Set("salt", salt)
	q.Set("sign", hex.EncodeToString(sum[:]))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baiduEndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return providerOutput{}, err
	}
	var resp baiduResponse
	if err := doJSON(client, req, &resp); err != nil {
		return providerOutput{}, err
	}
	if resp.ErrorCode != "" && resp.ErrorCode != "0" {
		return providerOutput{}, fmt.Errorf("%s（%s）", baiduErrorText(resp.ErrorCode), resp.ErrorCode)
	}
	if len(resp.TransResult) == 0 {
		return providerOutput{}, errors.New("百度翻译没有返回译文")
	}
	var b strings.Builder
	for _, r := range resp.TransResult {
		b.WriteString(r.Dst)
	}
	return providerOutput{Text: b.String(), Detected: resp.From}, nil
}

// baiduLang 把界面里的语言代码换成百度接口的代码。百度没有 zh-CN，
// 简体中文就是 zh，繁体是 cht，日语是 jp，韩语是 kor。
func baiduLang(target string) string {
	switch target {
	case "zh-CN", "zh", "auto":
		return "zh"
	case "zh-TW":
		return "cht"
	case "en":
		return "en"
	case "ja":
		return "jp"
	case "ko":
		return "kor"
	}
	return "zh"
}

func baiduErrorText(code string) string {
	switch code {
	case "52001":
		return "请求超时，请重试"
	case "52002":
		return "百度翻译系统错误"
	case "52003":
		return "未授权用户，请检查 App ID"
	case "54000":
		return "必填参数为空"
	case "54001":
		return "签名错误，请检查 App 密钥"
	case "54003":
		return "访问频率受限，请稍后再试"
	case "54004":
		return "账户余额不足"
	case "54005":
		return "长文本请求过于频繁"
	case "58000":
		return "客户端 IP 非法"
	case "58001":
		return "不支持该语言方向"
	case "58002":
		return "服务已关闭"
	}
	return "百度翻译返回错误"
}

// ---------------- Google ----------------

type googleResponse struct {
	Data struct {
		Translations []struct {
			TranslatedText         string `json:"translatedText"`
			DetectedSourceLanguage string `json:"detectedSourceLanguage"`
		} `json:"translations"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func translateGoogle(ctx context.Context, client *http.Client, p Provider, text, target string) (providerOutput, error) {
	key := p.Credentials["apiKey"]
	if key == "" {
		return providerOutput{}, errors.New("缺少 Google API Key")
	}
	body, err := json.Marshal(map[string]any{
		"q":      text,
		"target": googleLang(target),
		"format": "text",
	})
	if err != nil {
		return providerOutput{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		googleEndpoint+"?key="+url.QueryEscape(key), bytes.NewReader(body))
	if err != nil {
		return providerOutput{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp googleResponse
	if err := doJSON(client, req, &resp); err != nil {
		return providerOutput{}, err
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return providerOutput{}, errors.New("Google 翻译：" + resp.Error.Message)
	}
	if len(resp.Data.Translations) == 0 {
		return providerOutput{}, errors.New("Google 翻译没有返回译文")
	}
	t := resp.Data.Translations[0]
	// 即使指定 format=text，Google 仍可能把 & 等字符转成 HTML 实体。
	return providerOutput{
		Text:     html.UnescapeString(t.TranslatedText),
		Detected: t.DetectedSourceLanguage,
	}, nil
}

// googleLang 把界面语言代码映射成 Google 的代码；Google 本身支持 zh-CN / zh-TW。
func googleLang(target string) string {
	if target == "auto" {
		return "zh-CN"
	}
	return target
}

// ---------------- OpenAI 兼容 ----------------

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func translateOpenAI(ctx context.Context, client *http.Client, p Provider, text, target string) (providerOutput, error) {
	key := p.Credentials["apiKey"]
	if key == "" {
		return providerOutput{}, errors.New("缺少 API Key")
	}
	endpoint := chatCompletionsURL(p.BaseURL)
	if endpoint == "" {
		return providerOutput{}, errors.New("缺少 Base URL")
	}
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gpt-4.1-mini"
	}

	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a translation engine. Translate the user's text into " + langEnglishName(target) + ". Output only the translation, without explanations, notes, or quotes."},
			{"role": "user", "content": text},
		},
		"temperature": 0,
		"stream":      false,
	})
	if err != nil {
		return providerOutput{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return providerOutput{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	var resp openAIResponse
	if err := doJSON(client, req, &resp); err != nil {
		return providerOutput{}, err
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return providerOutput{}, errors.New(resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return providerOutput{}, errors.New("服务没有返回翻译结果")
	}
	out := strings.TrimSpace(resp.Choices[0].Message.Content)
	if out == "" {
		return providerOutput{}, errors.New("服务返回了空译文")
	}
	return providerOutput{Text: out}, nil
}

// chatCompletionsURL 兼容用户在 Base URL 里写不写 /v1、写不写完整路径。
func chatCompletionsURL(base string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		return ""
	}
	if strings.HasSuffix(b, "/chat/completions") {
		return b
	}
	return b + "/chat/completions"
}

// ---------------- 公共 ----------------

// doJSON 发送请求并把成功响应解到 out，HTTP 状态异常时带上响应片段。
func doJSON(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 有些服务把错误信息放在 JSON 里，这里先尝试解析出更具体的原因。
		var probe struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &probe) == nil && probe.Error != nil && probe.Error.Message != "" {
			return fmt.Errorf("HTTP %d：%s", resp.StatusCode, probe.Error.Message)
		}
		return fmt.Errorf("HTTP %d：%s", resp.StatusCode, snippet(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析响应失败：%w", err)
	}
	return nil
}

func snippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// ---------------- 语言判断 ----------------

// resolveTargetLang 计算这次翻译的目标语言：“自动”时按原文是否含中文决定，
// 中文原文译成英语，其他语言译成简体中文。
func resolveTargetLang(pref, text string) string {
	if pref != "" && pref != "auto" {
		return pref
	}
	if isLikelyChinese(text) {
		return "en"
	}
	return "zh-CN"
}

// isLikelyChinese 判断原文是否以中文为主。
// 不能只看“有没有汉字”：日文几乎都含汉字，直接按汉字判断会把日文
// 误判成中文、译成英文。出现假名或谚文时按日文/韩文处理。
func isLikelyChinese(text string) bool {
	han := 0
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			return false
		case unicode.Is(unicode.Hangul, r):
			return false
		case unicode.Is(unicode.Han, r):
			han++
		}
	}
	return han > 0
}

// langEnglishName 给 OpenAI 的提示词用自然语言描述目标语言。
func langEnglishName(code string) string {
	switch code {
	case "zh-CN":
		return "Simplified Chinese"
	case "zh-TW":
		return "Traditional Chinese"
	case "en":
		return "English"
	case "ja":
		return "Japanese"
	case "ko":
		return "Korean"
	}
	return "Simplified Chinese"
}
