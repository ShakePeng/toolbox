package main

import (
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
	SupplierID     string
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
		SupplierID:     p.SupplierID,
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
	case "google":
		if d.APIKey != "" {
			m["apiKey"] = d.APIKey
		}
	}
	return m
}

func (a *app) openProvider(i int) {
	d := newDraft(i, a.providers[i])
	if d.Kind == "openai-compatible" {
		a.ensureDraftSupplier(d)
	}
	a.draft = d
}

// enabledAISuppliers 返回可以在翻译里使用的 AI 供应商。
func (a *app) enabledAISuppliers() []AISupplier {
	if a == nil {
		return nil
	}
	var out []AISupplier
	for _, supplier := range a.settings.AISuppliers {
		if supplier.Enabled {
			out = append(out, supplier)
		}
	}
	return out
}

// ensureDraftSupplier 校正草稿里的供应商和模型，保证下拉有合法选中值。
func (a *app) ensureDraftSupplier(d *providerDraft) {
	suppliers := a.enabledAISuppliers()
	if len(suppliers) == 0 {
		d.SupplierID = ""
		return
	}
	valid := false
	for _, supplier := range suppliers {
		if supplier.ID == d.SupplierID {
			valid = true
			if d.Model == "" && len(supplier.Models) > 0 {
				d.Model = supplier.Models[0]
			}
			break
		}
	}
	if !valid {
		d.SupplierID = suppliers[0].ID
		if len(suppliers[0].Models) > 0 {
			d.Model = suppliers[0].Models[0]
		} else {
			d.Model = ""
		}
	}
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
	p.Model = d.Model
	if d.Kind == "openai-compatible" {
		// 地址和密钥统一由 AI 供应商提供，不再存在 Provider 里。
		p.SupplierID = d.SupplierID
		p.BaseURL = ""
		p.Credentials = map[string]string{}
	} else {
		p.BaseURL = d.BaseURL
		p.Credentials = d.credentials()
	}
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
		// Modal 自带面板背景和圆角，这里只排内容，避免再画一层卡片形成双层遮罩。
		ui.Column(c).Width(540).Gap(18).Padding(4).Children(func() {
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
					a.aiTranslationFields(c, d)
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
			hint := "空字段表示未配置；已保存的凭据会直接回显。填写新值会覆盖本机配置中的旧值。"
			if d.Kind == "openai-compatible" {
				hint = "AI 翻译的地址和密钥在请求时从所选 AI 供应商读取；修改请到「AI供应商」页。"
			}
			ui.Row(c).Padding(12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.04)).Children(func() {
				ui.Text(c, hint).FontSize(11).TextColor(colTextMuted)
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

// aiTranslationFields 是 AI 翻译的配置：从 AI供应商 里选择已启用的供应商
// 和模型，地址与密钥在发请求前解析。
func (a *app) aiTranslationFields(c *ui.Context, d *providerDraft) {
	suppliers := a.enabledAISuppliers()
	if len(suppliers) == 0 {
		ui.Column(c).Gap(8).Padding(12).Radius(10).
			Background(ui.RGBA(255, 255, 255, 0.035)).Border(1, colLine).Children(func() {
			ui.Text(c, "还没有启用的 AI 供应商").FontSize(12).Bold().TextColor(colTextSoft)
			ui.Text(c, "先到「AI供应商」页添加并启用一个 OpenAI 兼容服务。").
				FontSize(11).TextColor(colTextMuted)
			if ghostBtn(c, "去 AI供应商").Clicked() {
				a.draft = nil
				a.section = "ai"
			}
		})
		return
	}

	names := make([]string, 0, len(suppliers))
	current := ""
	for _, supplier := range suppliers {
		names = append(names, supplier.Name)
		if supplier.ID == d.SupplierID {
			current = supplier.Name
		}
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "供应商").FontSize(11).TextColor(colTextSoft)
		if ui.Select(c, &current, names).Changed() {
			for _, supplier := range suppliers {
				if supplier.Name != current {
					continue
				}
				d.SupplierID = supplier.ID
				d.Model = ""
				if len(supplier.Models) > 0 {
					d.Model = supplier.Models[0]
				}
				break
			}
		}
	})

	var models []string
	for _, supplier := range suppliers {
		if supplier.ID == d.SupplierID {
			models = supplier.Models
			break
		}
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "模型").FontSize(11).TextColor(colTextSoft)
		if len(models) == 0 {
			ui.Text(c, "这个供应商还没有模型，先到「AI供应商」页获取模型。").
				FontSize(11).TextColor(colTextMuted)
			return
		}
		ui.Select(c, &d.Model, models)
	})
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
