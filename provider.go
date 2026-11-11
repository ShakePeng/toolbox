package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// providerDraft 是 Provider 编辑弹窗的工作副本。
type providerDraft struct {
	idx            int
	ID             string
	Name           string
	Kind           string
	Enabled        bool
	UseSystemProxy bool
	BaseURL        string
	Model          string
	AppID          string
	Secret         string
	APIKey         string
}

func newDraft(idx int, p Provider) *providerDraft {
	return &providerDraft{
		idx:            idx,
		ID:             p.ID,
		Name:           p.Name,
		Kind:           p.Kind,
		Enabled:        p.Enabled,
		UseSystemProxy: p.UseSystemProxy,
		BaseURL:        p.BaseURL,
		Model:          p.Model,
		AppID:          p.Credentials["appId"],
		Secret:         p.Credentials["secret"],
		APIKey:         p.Credentials["apiKey"],
	}
}

func (d *providerDraft) credentials() map[string]string {
	m := map[string]string{}
	switch d.Kind {
	case "baidu":
		if d.AppID != "" {
			m["appId"] = d.AppID
		}
		if d.Secret != "" {
			m["secret"] = d.Secret
		}
	case "google", "openai-compatible":
		if d.APIKey != "" {
			m["apiKey"] = d.APIKey
		}
	}
	return m
}

func (a *app) openProvider(i int) {
	a.draft = newDraft(i, a.providers[i])
}

func (a *app) closeProvider() {
	a.draft = nil
}

func (a *app) saveProvider(test bool) {
	d := a.draft
	if d == nil {
		return
	}
	p := &a.providers[d.idx]
	p.Enabled = d.Enabled
	p.UseSystemProxy = d.UseSystemProxy
	p.BaseURL = d.BaseURL
	p.Model = d.Model
	p.Credentials = d.credentials()
	p.Configured = providerConfigured(*p)
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	name := d.Name
	a.closeProvider()
	if test {
		a.testProvider(d.idx)
	} else {
		a.toast(name + "：配置已保存")
	}
}

func (a *app) providerModal(c *ui.Context) {
	if a.draft == nil {
		return
	}
	d := a.draft
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(540).Gap(18).Padding(24).Radius(20).Background(colBgElevated).
			Border(1, colLineStrong).Shadow(0, 32, 100, 0, ui.RGBA(0, 0, 0, 0.5)).Children(func() {
			// 头部
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, "CONFIGURE").FontSize(11).Bold().TextColor(colTextMuted).LetterSpacing(1.4)
					ui.Text(c, d.Name).FontSize(21).Bold()
				})
				closeBtn := ghostBtn(c, "").Label("关闭").Size(34, 34).Center()
				closeBtn.Children(func() {
					ui.Icon(c, icon("close")).FontSize(16).TextColor(colTextSoft)
				})
				if closeBtn.Clicked() {
					a.closeProvider()
				}
			})

			// 字段
			ui.Column(c).Gap(14).Children(func() {
				switch d.Kind {
				case "baidu":
					a.modalField(c, "App ID", &d.AppID, "")
					a.modalField(c, "App 密钥", &d.Secret, "")
				case "google":
					a.modalField(c, "Google API Key", &d.APIKey, "")
				case "openai-compatible":
					a.modalField(c, "API Key", &d.APIKey, "")
					a.modalField(c, "Base URL", &d.BaseURL, "http://192.168.1.20:8000/v1")
					a.modalField(c, "模型", &d.Model, "gpt-4.1-mini")
					if strings.HasPrefix(strings.ToLower(strings.TrimSpace(d.BaseURL)), "http://") {
						ui.Row(c).Gap(7).AlignItems(ui.Start).Padding(9, 10).Radius(9).
							Background(ui.RGBA(255, 107, 104, 0.09)).Children(func() {
							ui.Icon(c, icon("alert")).FontSize(13).TextColor(colDanger)
							ui.Text(c, "HTTP 会明文传输 API Key，请仅用于可信内网或本机服务。").
								FontSize(11).TextColor(colDanger)
						})
					}
				}
			})

			// 申请指引放在字段附近，用户不用离开配置流程去猜所需凭证。
			a.providerCredentialGuide(c, d.Kind)

			// 走系统代理
			ui.Row(c).AlignItems(ui.Center).Gap(18).Padding(13, 14).Radius(11).
				Background(ui.RGBA(255, 255, 255, 0.035)).Border(1, colLine).Children(func() {
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					ui.Text(c, "走系统代理").FontSize(13)
					ui.Text(c, "默认关闭；仅海外服务无法直连时再按需开启。").FontSize(11).TextColor(colTextMuted)
				})
				ui.Switch(c, &d.UseSystemProxy).Label("走系统代理")
			})

			// 提示
			ui.Row(c).Padding(12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.04)).Children(func() {
				ui.Text(c, "空字段表示未配置；已保存的凭据会直接回显。填写新值会覆盖本机配置中的旧值。").
					FontSize(11).TextColor(colTextMuted)
			})

			// 动作
			ui.Row(c).AlignItems(ui.Center).Gap(18).Children(func() {
				ui.Checkbox(c, &d.Enabled, "保存后启用此服务")
				ui.Spacer(c)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ghostBtn(c, "取消").Clicked() {
						a.closeProvider()
					}
					if secondaryBtn(c, "保存并测试").Clicked() {
						a.saveProvider(true)
					}
					if primaryBtn(c, "保存配置").Clicked() {
						a.saveProvider(false)
					}
				})
			})
		})
	})
	if !open {
		a.closeProvider()
	}
}

func (a *app) modalField(c *ui.Context, label string, val *string, placeholder string) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(colTextSoft)
		ui.TextInput(c, val).Placeholder(placeholder).Label(label).Height(40).WidthPercent(100)
	})
}

// providerCredentialGuide 给出各服务实际使用的凭证类型与官方申请入口。
// Google 用的是 Translation Basic 的 API Key，不引导用户创建服务账号 JSON。
func (a *app) providerCredentialGuide(c *ui.Context, kind string) {
	var title, steps, linkLabel, url string
	switch kind {
	case "baidu":
		title = "需要：APP ID 和密钥"
		steps = "登录百度翻译开放平台，注册开发者并开通通用文本翻译；在管理控制台创建应用后，将 APP ID 和密钥填入上方。"
		linkLabel = "打开百度翻译接入指南"
		url = "https://fanyi-api.baidu.com/doc/23"
	case "google":
		title = "需要：Google Cloud API Key"
		steps = "在 Google Cloud 创建项目、启用结算和 Cloud Translation API；进入“凭据”页创建 API Key，建议仅允许 Cloud Translation API。"
		linkLabel = "打开 Google Cloud 凭据页"
		url = "https://console.cloud.google.com/apis/credentials"
	default:
		return
	}
	ui.Column(c).Gap(6).Padding(12, 14).Radius(10).
		Background(ui.RGBA(255, 255, 255, 0.035)).Border(1, colLine).Children(func() {
		ui.Text(c, title).FontSize(12).Bold().TextColor(colTextSoft)
		ui.Text(c, steps).FontSize(11).TextColor(colTextMuted).LineHeight(1.5)
		ui.Link(c, linkLabel, url).FontSize(12)
	})
}
