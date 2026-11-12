package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// aiSupplierDraft 是 AI 供应商编辑弹窗的工作副本。idx 为 -1 表示新增。
type aiSupplierDraft struct {
	idx       int
	ID        string
	Name      string
	BaseURL   string
	APIKey    string
	Enabled   bool
	Available []string
	Selected  map[string]bool
	NewModel  string
	Fetching  bool
	FetchErr  string
}

func newAISupplierDraft(idx int, s AISupplier) *aiSupplierDraft {
	d := &aiSupplierDraft{
		idx:       idx,
		ID:        s.ID,
		Name:      s.Name,
		BaseURL:   s.BaseURL,
		APIKey:    s.APIKey,
		Enabled:   s.Enabled,
		Available: append([]string(nil), s.Models...),
		Selected:  make(map[string]bool, len(s.Models)),
	}
	for _, model := range s.Models {
		d.Selected[model] = true
	}
	return d
}

func (d *aiSupplierDraft) hasModel(model string) bool {
	for _, m := range d.Available {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	return false
}

// mergeModels 把获取到的模型并入列表，新模型默认勾选。
func (d *aiSupplierDraft) mergeModels(models []string) {
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || d.hasModel(model) {
			continue
		}
		d.Available = append(d.Available, model)
		d.Selected[model] = true
	}
	sort.Strings(d.Available)
}

func (d *aiSupplierDraft) addModel(model string) {
	model = strings.TrimSpace(model)
	if model == "" || d.hasModel(model) {
		return
	}
	d.Available = append(d.Available, model)
	d.Selected[model] = true
}

func (d *aiSupplierDraft) selectedModels() []string {
	var out []string
	for _, model := range d.Available {
		if d.Selected[model] {
			out = append(out, model)
		}
	}
	return out
}

// ---------------- 获取模型 ----------------

// fetchAISupplierModels 允许测试替换，默认请求 OpenAI 兼容的 /models。
var fetchAISupplierModels = fetchOpenAIModels

func fetchOpenAIModels(baseURL, apiKey string) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("请先填写 Base URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("Base URL 无效：%w", err)
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := newHTTPClient(false).Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求模型列表失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取模型列表失败：%w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("请求模型列表失败（HTTP %d）", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("返回内容不是 OpenAI 兼容的模型列表")
	}
	var models []string
	for _, item := range payload.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	sort.Strings(models)
	return models, nil
}

// startAISupplierModelFetch 在后台获取模型列表。请求期间关闭弹窗时，结果
// 会因为 draft 指针不同被丢弃。
func (a *app) startAISupplierModelFetch() {
	d := a.aiDraft
	if d == nil || d.Fetching {
		return
	}
	base := strings.TrimSpace(d.BaseURL)
	if base == "" {
		d.FetchErr = "请先填写 Base URL"
		return
	}
	key := strings.TrimSpace(d.APIKey)
	d.Fetching = true
	d.FetchErr = ""

	apply := func(models []string, err error) {
		if err != nil {
			d.FetchErr = err.Error()
			return
		}
		d.mergeModels(models)
		if len(models) == 0 {
			d.FetchErr = "服务没有返回模型"
		}
	}

	// 无窗口（单元测试）时同步执行，避免依赖事件循环。
	if a.win == nil {
		models, err := fetchAISupplierModels(base, key)
		d.Fetching = false
		apply(models, err)
		return
	}
	go func() {
		models, err := fetchAISupplierModels(base, key)
		a.win.Update(func() {
			if a.aiDraft != d {
				return
			}
			d.Fetching = false
			apply(models, err)
		})
	}()
}

// ---------------- 页面 ----------------

func (a *app) aiSection(c *ui.Context) {
	a.intro(c, "AI SUPPLIERS", "AI供应商", "管理 OpenAI 兼容的 AI 服务，配置地址、密钥和模型。", func() {
		pill(c, fmt.Sprintf("%d 个供应商", len(a.settings.AISuppliers)), colAccentSoft, colAccentStr)
	})

	if len(a.settings.AISuppliers) == 0 {
		ui.Column(c).Padding(30).Radius(17).Background(colCard).Border(1, colLine).
			AlignItems(ui.Center).Gap(10).Shadow(0, 2, 12, 0, shadowCard).Children(func() {
			iconTile(c, "provider", 42, 12, colBgSoft, colTextSoft)
			ui.Text(c, "还没有 AI 供应商").FontSize(15).Bold()
			ui.Text(c, "添加一个 OpenAI 兼容服务，填写 Base URL、API Key 和模型。").
				FontSize(12).TextColor(colTextMuted)
			if primaryBtn(c, "添加供应商").Clicked() {
				a.openAISupplier(-1)
			}
		})
		return
	}

	ui.Column(c).Gap(12).Children(func() {
		for i := range a.settings.AISuppliers {
			a.aiSupplierCard(c, i)
		}
	})
	ui.Row(c).Children(func() {
		if secondaryBtn(c, "添加供应商").Clicked() {
			a.openAISupplier(-1)
		}
	})
}

func (a *app) aiSupplierCard(c *ui.Context, i int) {
	s := &a.settings.AISuppliers[i]
	bc := colLine
	if s.Enabled {
		bc = ui.RGBA(245, 158, 11, 0.35)
	}
	ui.Column(c).Key("ai-supplier-"+s.ID).Padding(16).Radius(15).Background(colCard).
		Border(1, bc).Shadow(0, 2, 12, 0, shadowCard).Gap(12).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			iconTile(c, "provider", 38, 11, colBgSoft, colTextSoft)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, s.Name).FontSize(15).Bold()
				ui.Text(c, s.BaseURL).FontSize(12).TextColor(colTextMuted).MaxLines(1)
			})
			if ui.Switch(c, &s.Enabled).Label(s.Name + "启用").Changed() {
				a.afterAISupplierChange()
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if len(s.Models) > 0 {
				pill(c, fmt.Sprintf("%d 个模型", len(s.Models)), colBgSoft, colTextSoft)
			} else {
				pill(c, "未配置模型", colBgSoft, colTextSoft)
			}
			ui.Spacer(c)
			if ghostBtn(c, "编辑").Label("编辑 " + s.Name).Clicked() {
				a.openAISupplier(i)
			}
			if ghostBtn(c, "删除").Label("删除 " + s.Name).Clicked() {
				a.removeAISupplier(i)
			}
		})
	})
}

// ---------------- 编辑弹窗 ----------------

func (a *app) openAISupplier(i int) {
	if i >= 0 && i < len(a.settings.AISuppliers) {
		a.aiDraft = newAISupplierDraft(i, a.settings.AISuppliers[i])
		return
	}
	a.aiDraft = &aiSupplierDraft{
		idx:      -1,
		Enabled:  true,
		Selected: map[string]bool{},
	}
}

func (a *app) saveAISupplier() {
	d := a.aiDraft
	if d == nil {
		return
	}
	name := strings.TrimSpace(d.Name)
	if name == "" {
		a.toast("请填写供应商名称")
		return
	}
	base := strings.TrimSpace(d.BaseURL)
	if base == "" {
		a.toast("请填写 Base URL")
		return
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		a.toast("Base URL 需以 http:// 或 https:// 开头")
		return
	}
	s := AISupplier{
		ID:      d.ID,
		Name:    name,
		BaseURL: base,
		APIKey:  strings.TrimSpace(d.APIKey),
		Models:  d.selectedModels(),
		Enabled: d.Enabled,
	}
	if d.idx >= 0 && d.idx < len(a.settings.AISuppliers) {
		s.ID = a.settings.AISuppliers[d.idx].ID
		a.settings.AISuppliers[d.idx] = s
	} else {
		if s.ID == "" {
			s.ID = fmt.Sprintf("ai-%d", time.Now().UnixNano())
		}
		a.settings.AISuppliers = append(a.settings.AISuppliers, s)
	}
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	a.aiDraft = nil
	a.toast(name + "：已保存")
}

func (a *app) removeAISupplier(i int) {
	if i < 0 || i >= len(a.settings.AISuppliers) {
		return
	}
	name := a.settings.AISuppliers[i].Name
	a.settings.AISuppliers = append(a.settings.AISuppliers[:i], a.settings.AISuppliers[i+1:]...)
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	a.toast("已删除 " + name)
}

func (a *app) afterAISupplierChange() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
	}
}

func (a *app) aiSupplierModal(c *ui.Context) {
	d := a.aiDraft
	if d == nil {
		return
	}
	open := true
	// 窗口较矮时字段区自己滚动，保证底部的保存按钮始终可见。
	_, winH := c.Size()
	formMax := winH - 280
	if formMax < 220 {
		formMax = 220
	}
	ui.Modal(c, &open, func() {
		// Modal 自带面板背景和圆角，这里只排内容，避免再画一层卡片形成双层遮罩。
		ui.Column(c).Width(540).Gap(16).Padding(4).Children(func() {
			title := "添加供应商"
			if d.idx >= 0 {
				title = "编辑供应商"
			}
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, "AI SUPPLIER").FontSize(11).Bold().TextColor(colTextMuted).LetterSpacing(1.4)
					ui.Text(c, title).FontSize(21).Bold()
				})
				closeBtn := ghostBtn(c, "").Label("关闭").Size(34, 34).Center()
				closeBtn.Children(func() {
					ui.Icon(c, icon("close")).FontSize(16).TextColor(colTextSoft)
				})
				if closeBtn.Clicked() {
					a.aiDraft = nil
				}
			})

			ui.Scroll(c).MaxHeight(formMax).Children(func() {
				ui.Column(c).Gap(14).Children(func() {
					a.modalField(c, "名称", &d.Name, "如 OpenAI / 我的中转站")
					a.modalField(c, "Base URL", &d.BaseURL, "https://api.openai.com/v1")
					a.modalField(c, "API Key", &d.APIKey, "sk-...")
					a.aiSupplierModelPicker(c, d)
					if strings.HasPrefix(strings.ToLower(strings.TrimSpace(d.BaseURL)), "http://") {
						ui.Row(c).Gap(7).AlignItems(ui.Start).Padding(9, 10).Radius(9).
							Background(ui.RGBA(255, 107, 104, 0.09)).Children(func() {
							ui.Icon(c, icon("alert")).FontSize(13).TextColor(colDanger)
							ui.Text(c, "HTTP 会明文传输 API Key，请仅用于可信内网或本机服务。").
								FontSize(11).TextColor(colDanger)
						})
					}
				})
			})

			ui.Row(c).Padding(12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.04)).Children(func() {
				ui.Text(c, "API Key 和翻译凭据一样明文保存在本机 config.json，不会上传到其他地方。").
					FontSize(11).TextColor(colTextMuted)
			})

			ui.Row(c).AlignItems(ui.Center).Gap(18).Children(func() {
				ui.Checkbox(c, &d.Enabled, "启用此供应商")
				ui.Spacer(c)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ghostBtn(c, "取消").Clicked() {
						a.aiDraft = nil
					}
					if primaryBtn(c, "保存").Clicked() {
						a.saveAISupplier()
					}
				})
			})
		})
	})
	if !open {
		a.aiDraft = nil
	}
}

// aiSupplierModelPicker 是弹窗里的模型多选区域：从服务获取模型，或手动
// 添加；勾选的模型会保存到供应商配置。
func (a *app) aiSupplierModelPicker(c *ui.Context, d *aiSupplierDraft) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "模型").FontSize(11).TextColor(colTextSoft)
				hint := "从服务获取后勾选需要的模型。"
				if d.FetchErr != "" {
					hint = "获取失败时可以手动添加模型。"
				}
				ui.Text(c, hint).FontSize(11).TextColor(colTextMuted)
			})
			label := "获取模型"
			if d.Fetching {
				label = "获取中…"
			}
			if secondaryBtn(c, label).Label("获取模型列表").Clicked() && !d.Fetching {
				a.startAISupplierModelFetch()
			}
		})

		if d.FetchErr != "" {
			ui.Text(c, d.FetchErr).FontSize(11).TextColor(colDanger)
		}

		if len(d.Available) == 0 {
			ui.Row(c).Padding(10, 12).Radius(9).Background(ui.RGBA(255, 255, 255, 0.035)).
				Border(1, colLine).Children(func() {
				ui.Text(c, "还没有模型，点击「获取模型」或手动添加。").FontSize(11).TextColor(colTextMuted)
			})
		} else {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if ghostBtn(c, "全选").Clicked() {
					for _, model := range d.Available {
						d.Selected[model] = true
					}
				}
				if ghostBtn(c, "清空").Clicked() {
					for _, model := range d.Available {
						d.Selected[model] = false
					}
				}
				ui.Spacer(c)
				ui.Text(c, fmt.Sprintf("已选 %d / %d", len(d.selectedModels()), len(d.Available))).
					FontSize(11).TextColor(colTextMuted)
			})
			ui.Row(c).Wrap().Gap(6).Children(func() {
				for _, model := range d.Available {
					selected := d.Selected[model]
					bg, border, fg := colBgSoft, colLine, colTextSoft
					if selected {
						bg, border, fg = colAccentSoft, colAccentStr, colAccentStr
					}
					chip := ui.Row(c).Padding(6, 12).Radius(999).Background(bg).
						Border(1, border).AlignItems(ui.Center).Gap(5)
					chip.Label("选择模型 " + model)
					chip.Children(func() {
						if selected {
							ui.Icon(c, icon("check")).FontSize(11).TextColor(colAccentStr)
						}
						ui.Text(c, model).FontSize(12).TextColor(fg)
					})
					if chip.Clicked() {
						d.Selected[model] = !selected
					}
				}
			})
		}

		// 获取失败或服务没有返回模型时，才提供手动添加作为兜底。
		if d.FetchErr != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if in := ui.TextInput(c, &d.NewModel).
					Placeholder("手动添加，如 gpt-4o").Height(36).Grow(1); in.Changed() {
					// 输入过程中只更新草稿，点「添加」才进入列表。
				}
				if secondaryBtn(c, "添加").Label("手动添加模型").Clicked() {
					d.addModel(d.NewModel)
					d.NewModel = ""
				}
			})
		}
	})
}
