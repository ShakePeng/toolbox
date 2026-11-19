package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"
)

const (
	tinyPending    = "pending"
	tinyProcessing = "processing"
	tinyDone       = "done"
	tinyError      = "error"
)

// 图片压缩是独立工具窗口，沿用参考工具的浅色工作台，而不是功能中心的深色设置页。
var (
	tinyBg         = ui.Hex("#f3f6fa")
	tinySurface    = ui.Hex("#ffffff")
	tinySoft       = ui.Hex("#f8fafc")
	tinyHover      = ui.Hex("#f1f5f9")
	tinyLine       = ui.Hex("#e2e8f0")
	tinyLineStrong = ui.Hex("#cbd5e1")
	tinyText       = ui.Hex("#0f172a")
	tinyTextSoft   = ui.Hex("#475569")
	tinyTextMuted  = ui.Hex("#64748b")
	tinyAccent     = ui.Hex("#0891b2")
	tinyAccentStr  = ui.Hex("#0e7490")
	tinyAccentSoft = ui.Hex("#ecfeff")
	tinyDanger     = ui.Hex("#dc2626")
	tinySuccess    = ui.Hex("#059669")
)

// 尺寸按 8 点栅格：间距只取 4/8/16/24，按钮高度统一，避免各处随手写
// 9、13 这类数值把窗口撑散、同一行按钮高低不齐。
const (
	tinyGap       = 8
	tinyBtnHeight = 32
	tinyBtnRadius = 8
	tinyBtnPadX   = 12
)

func imageCompressTheme() *ui.Theme {
	t := ui.LightTheme()
	t.Background = tinyBg
	t.Surface = tinySoft
	t.SurfaceHover = tinyHover
	t.SurfacePressed = tinyLine
	t.Border = tinyLine
	t.Text = tinyText
	t.TextMuted = tinyTextMuted
	t.Accent = tinyAccent
	t.InactiveAccent = tinyAccent
	t.AccentHover = tinyAccentStr
	t.AccentPressed = tinyAccentStr
	t.AccentText = tinySurface
	t.Danger = tinyDanger
	t.Success = tinySuccess
	t.Selection = ui.RGBA(8, 145, 178, 0.22)
	t.Focus = tinyAccent
	t.Radius = 9
	return t
}

func tinyPrimaryBtn(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Background(tinyAccent).TextColor(tinySurface).Bold().
		MinHeight(tinyBtnHeight).Radius(tinyBtnRadius).Padding(0, tinyBtnPadX)
}

func tinySecondaryBtn(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Background(tinySurface).TextColor(tinyTextSoft).
		Border(1, tinyLine).MinHeight(tinyBtnHeight).Radius(tinyBtnRadius).Padding(0, tinyBtnPadX)
}

// tinyGhostBtn 与另两个按钮同高同内边距，只是没有底色和描边；
// 尺寸不一致时同一行的按钮会高低不齐。
func tinyGhostBtn(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Background(ui.Transparent).TextColor(tinyTextMuted).
		MinHeight(tinyBtnHeight).Radius(tinyBtnRadius).Padding(0, tinyBtnPadX)
}

type imageCompressItem struct {
	ID           int
	SourcePath   string
	Format       string
	OriginalSize int64
	Data         []byte
	Width        int
	Height       int
	Thumb        *ui.Bitmap
	Status       string
	Err          string
	Selected     bool
	Saving       bool
	Generation   int
}

// imageCompressSession 只从主线程修改队列；后台任务拿到的是设置和路径快照，
// 返回时必须匹配 ID 与 Generation，避免重试/移除后的旧结果污染新任务。
type imageCompressSession struct {
	app      *app
	win      *mygo.Window
	ctx      context.Context
	cancel   context.CancelFunc
	items    []*imageCompressItem
	nextID   int
	running  bool
	activeID int
	notice   string
	zipping  bool

	// settingsOpen 控制压缩设置弹框；设置不再占一整页，避免在工具页和设置页之间来回切换。
	settingsOpen bool

	jpegQualityText string
	webpQualityText string
	pngLevelText    string
	maxEdgeText     string
	newKey          string
}

func (a *app) showImageCompressor() {
	if !a.settings.ImageCompress.Enabled {
		a.notify("图片压缩未开启，请先在功能中心打开")
		return
	}
	if a.compressor != nil {
		a.compressor.win.Show()
		a.compressor.win.Focus()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &imageCompressSession{app: a, ctx: ctx, cancel: cancel}
	s.syncSettingDrafts()
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:         "图片压缩",
		Width:         1000,
		Height:        760,
		DisableResize: true,
		Content:       ui.View(s.view),
	})
	s.win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		s.win.Hide()
	})
	a.compressor = s
	s.win.Show()
	s.win.Focus()
}

// afterImageCompressToggle 同步启动器命令；关闭时连已打开的窗口一起收起。
func (a *app) afterImageCompressToggle() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if !a.settings.ImageCompress.Enabled && a.compressor != nil {
		a.compressor.win.Hide()
	}
	if a.launcher != nil {
		a.launcher.sourcesChanged()
	}
	if a.settings.ImageCompress.Enabled {
		a.toast("图片压缩已开启")
	} else {
		a.toast("图片压缩已关闭，tiny 命令已停用")
	}
}

func (s *imageCompressSession) syncSettingDrafts() {
	opts := compressOptionsFromSettings(s.app.settings.ImageCompress)
	s.jpegQualityText = strconv.Itoa(opts.JPEGQuality)
	s.webpQualityText = strconv.Itoa(opts.WebPQuality)
	s.pngLevelText = strconv.Itoa(opts.PNGLevel)
	// 0 表示不限制，直接显示 0 比留空更好理解。
	s.maxEdgeText = strconv.Itoa(opts.MaxEdge)
}

func (s *imageCompressSession) chooseImages() {
	win := s.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: win, Title: "选择要压缩的图片", Multiple: true,
			Filters: []mygo.FileFilter{{Name: "PNG / JPEG / WebP", Extensions: []string{"png", "jpg", "jpeg", "webp"}}},
		})
		win.Update(func() {
			if err != nil {
				s.notice = "选择图片失败：" + err.Error()
				return
			}
			s.addPaths(paths)
		})
	}()
}

// addPaths 同时服务文件对话框和拖放，保证两种入口使用同一套去重与格式检查。
func (s *imageCompressSession) addPaths(paths []string) {
	added, rejected := 0, 0
	var addedItems []*imageCompressItem
	existing := make(map[string]bool, len(s.items))
	for _, item := range s.items {
		existing[item.SourcePath] = true
	}
	for _, name := range paths {
		format, err := imageFormatForPath(name)
		if err != nil {
			rejected++
			continue
		}
		absolute, err := filepath.Abs(name)
		if err != nil {
			rejected++
			continue
		}
		absolute = filepath.Clean(absolute)
		if existing[absolute] {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() {
			rejected++
			continue
		}
		s.nextID++
		item := &imageCompressItem{
			ID: s.nextID, SourcePath: absolute, Format: format, OriginalSize: info.Size(),
			Status: tinyPending, Selected: true,
		}
		s.items = append(s.items, item)
		addedItems = append(addedItems, item)
		existing[absolute] = true
		added++
	}
	// 缩略图后台补；队列行先以文件名占位，避免选完图后一段空白等待。
	go s.loadThumbnails(addedItems)
	if rejected > 0 {
		s.notice = fmt.Sprintf("已添加 %d 张，跳过 %d 个不支持或不可读的文件", added, rejected)
	} else if added > 0 {
		s.notice = ""
	}
	// 选图只进入待压缩队列，由用户点击「开始压缩」；避免文件对话框一关闭就上传云端。
}

// startAll 只处理点击时已等待的图片；处理过程中新加入的图片会接着当前批次执行。
func (s *imageCompressSession) startAll() {
	if s.running {
		s.notice = "正在压缩，请等待当前批次完成"
		return
	}
	pending := false
	for _, item := range s.items {
		if item.Status == tinyPending {
			pending = true
			break
		}
	}
	if !pending {
		s.notice = "请先选择图片，或添加需要重新压缩的图片"
		return
	}
	s.notice = ""
	s.processNext(true)
}

// startOne 服务队列行的「压缩」按钮，只启动这一张，不连带执行其他等待项。
func (s *imageCompressSession) startOne(id int) {
	if s.running {
		s.notice = "正在压缩其他图片，请稍候"
		return
	}
	item := s.item(id)
	if item == nil || (item.Status != tinyPending && item.Status != tinyError) {
		return
	}
	item.Status, item.Err = tinyPending, ""
	item.Generation++
	s.notice = ""
	s.processItem(item, false)
}

func (s *imageCompressSession) processNext(continuous bool) {
	if s.running || s.ctx.Err() != nil {
		return
	}
	for _, item := range s.items {
		if item.Status == tinyPending {
			s.processItem(item, continuous)
			return
		}
	}
}

func (s *imageCompressSession) processItem(next *imageCompressItem, continuous bool) {
	settings := s.app.settings.ImageCompress
	if settings.Engine == "tinypng" && !hasTinyPNGKey(settings.TinyPNGKeys) {
		// 批量开始时一次性标记所有等待项，不为每张图重复发起必然失败的请求。
		if continuous {
			for _, item := range s.items {
				if item.Status == tinyPending {
					item.Status = tinyError
					item.Err = "请先在压缩设置中添加 TinyPNG API Key"
				}
			}
		} else {
			next.Status = tinyError
			next.Err = "请先在压缩设置中添加 TinyPNG API Key"
		}
		s.notice = "TinyPNG 云需要先配置 API Key"
		return
	}

	next.Status = tinyProcessing
	next.Err = ""
	next.Generation++
	s.running, s.activeID = true, next.ID
	id, generation := next.ID, next.Generation
	source, format := next.SourcePath, next.Format
	opts := compressOptionsFromSettings(settings)
	keys := append([]TinyPNGKey(nil), settings.TinyPNGKeys...)
	win := s.win

	go func() {
		input, err := os.ReadFile(source)
		var result imageCompressResult
		if err == nil {
			var engine imageCompressEngine = localImageCompressor{}
			if settings.Engine == "tinypng" {
				engine = newTinyPNGCompressor(nil, keys)
			}
			result, err = engine.Compress(s.ctx, input, format, opts)
		} else {
			err = fmt.Errorf("读取文件失败：%w", err)
		}
		var thumb *ui.Bitmap
		if err == nil {
			thumb, err = thumbnailFromBytes(result.Data, format)
		}
		win.Update(func() {
			if item := s.item(id); item != nil && item.Generation == generation {
				if err != nil {
					item.Status, item.Err = tinyError, err.Error()
					item.Data, item.Thumb = nil, nil
				} else {
					item.Status = tinyDone
					item.Data, item.Thumb = result.Data, thumb
					item.Width, item.Height = result.Width, result.Height
					if result.KeyIndex >= 0 && result.KeyIndex < len(s.app.settings.ImageCompress.TinyPNGKeys) {
						s.app.settings.ImageCompress.TinyPNGKeys[result.KeyIndex].Usage = result.Usage
						_ = s.app.persist()
					}
				}
			}
			s.running, s.activeID = false, 0
			if continuous {
				s.processNext(true)
			}
		})
	}()
}

func hasTinyPNGKey(keys []TinyPNGKey) bool {
	for _, item := range keys {
		if strings.TrimSpace(item.Key) != "" {
			return true
		}
	}
	return false
}

func (s *imageCompressSession) item(id int) *imageCompressItem {
	for _, item := range s.items {
		if item.ID == id {
			return item
		}
	}
	return nil
}

func (s *imageCompressSession) retry(id int) {
	s.startOne(id)
}

func (s *imageCompressSession) remove(id int) {
	for i, item := range s.items {
		if item.ID == id {
			item.Generation++
			s.items = append(s.items[:i], s.items[i+1:]...)
			return
		}
	}
}

func (s *imageCompressSession) clearQueue() {
	if s.running {
		s.notice = "当前图片压缩完成后才能清空队列"
		return
	}
	for _, item := range s.items {
		item.Generation++
	}
	s.items = nil
	s.notice = ""
}

func (s *imageCompressSession) download(id int, overwrite bool) {
	item := s.item(id)
	if item == nil || item.Status != tinyDone || item.Saving {
		return
	}
	item.Saving = true
	source, format := item.SourcePath, item.Format
	data := append([]byte(nil), item.Data...)
	win := s.win
	go func() {
		if overwrite {
			choice, err := mygo.Dialog.Message(mygo.MessageOptions{
				Parent: win, Type: mygo.MessageWarning, Title: "覆盖原图",
				Message: "确定用压缩结果覆盖原图吗？", Detail: filepath.Base(source) + "\n此操作会替换原文件。",
				Buttons: []string{"取消", "覆盖原图"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1},
			})
			if err != nil || choice.Button != 1 {
				win.Update(func() {
					if current := s.item(id); current != nil {
						current.Saving = false
					}
					if err != nil {
						s.notice = "覆盖确认失败：" + err.Error()
					}
				})
				return
			}
			err = overwriteTinyOriginal(source, format, data)
			win.Update(func() {
				if current := s.item(id); current != nil {
					current.Saving = false
					if err == nil {
						current.OriginalSize = int64(len(data))
					}
				}
				if err != nil {
					s.notice = "覆盖失败：" + err.Error()
				} else {
					s.notice = "已覆盖原图：" + filepath.Base(source)
				}
			})
			return
		}
		output, err := saveTinyBeside(source, data)
		win.Update(func() {
			if current := s.item(id); current != nil {
				current.Saving = false
			}
			if err != nil {
				s.notice = "保存失败：" + err.Error()
			} else {
				s.notice = "已保存：" + output
			}
		})
	}()
}

func (s *imageCompressSession) downloadZIP() {
	if s.zipping {
		return
	}
	var items []tinyZIPItem
	for _, item := range s.items {
		if item.Status == tinyDone && item.Selected {
			items = append(items, tinyZIPItem{Name: filepath.Base(item.SourcePath), Data: append([]byte(nil), item.Data...)})
		}
	}
	if len(items) == 0 {
		s.notice = "请先勾选已完成的图片"
		return
	}
	s.zipping = true
	win := s.win
	go func() {
		home, _ := os.UserHomeDir()
		defaultPath := filepath.Join(home, "compressed-"+time.Now().Format("20060102-150405")+".zip")
		output, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent: win, Title: "保存压缩图片 ZIP", DefaultPath: defaultPath,
			Filters: []mygo.FileFilter{{Name: "ZIP", Extensions: []string{"zip"}}},
		})
		if err == nil && output != "" {
			if !strings.EqualFold(filepath.Ext(output), ".zip") {
				output += ".zip"
			}
			err = writeTinyZIP(output, items)
		}
		win.Update(func() {
			s.zipping = false
			if err != nil {
				s.notice = "打包失败：" + err.Error()
			} else if output != "" {
				s.notice = "已打包：" + output
			}
		})
	}()
}

func (s *imageCompressSession) setEngine(engine string) {
	s.app.settings.ImageCompress.Engine = engine
	_ = s.app.persist()
	// 云端未配置时不要等用户逐张失败后才发现，直接弹出设置弹框。
	if engine == "tinypng" && !hasTinyPNGKey(s.app.settings.ImageCompress.TinyPNGKeys) {
		s.openSettings()
	}
}

func (s *imageCompressSession) saveNumberSetting(text string, field string) {
	if field == "maxEdge" && strings.TrimSpace(text) == "" {
		s.app.settings.ImageCompress.MaxEdge = 0
		_ = s.app.persist()
		return
	}
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return
	}
	cfg := &s.app.settings.ImageCompress
	switch field {
	case "jpeg":
		if value < 1 || value > 100 {
			return
		}
		cfg.JPEGQuality = value
	case "webp":
		if value < 1 || value > 100 {
			return
		}
		cfg.WebPQuality = value
	case "maxEdge":
		if value < 0 || value > 16384 {
			return
		}
		cfg.MaxEdge = value
	case "png":
		if value < 0 || value > 6 {
			return
		}
		cfg.PNGLevel = value
	}
	_ = s.app.persist()
}

// saveTinyPNGKey 只保留一把 Key：替换时沿用已记录的当月用量，避免误显示成新 Key 的额度。
func (s *imageCompressSession) saveTinyPNGKey() {
	key := strings.TrimSpace(s.newKey)
	if key == "" {
		return
	}
	cfg := &s.app.settings.ImageCompress
	usage := 0
	if len(cfg.TinyPNGKeys) > 0 && cfg.TinyPNGKeys[0].Key == key {
		usage = cfg.TinyPNGKeys[0].Usage
	}
	cfg.TinyPNGKeys = []TinyPNGKey{{Key: key, Usage: usage}}
	s.newKey = ""
	_ = s.app.persist()
}

// ---------------- 界面 ----------------

func (s *imageCompressSession) view(c *ui.Context) {
	ui.Themed(c, imageCompressTheme()).Fill().Background(tinyBg).Children(func() {
		ui.Column(c).Fill().Background(tinyBg).Children(func() {
			s.header(c)
			if s.notice != "" {
				ui.Row(c).Margin(tinyGap, 24, 0, 24).Padding(tinyGap, 12).Radius(8).Gap(tinyGap).
					Background(tinyAccentSoft).Border(1, ui.Hex("#a5f3fc")).Children(func() {
					ui.Icon(c, icon("alert")).FontSize(15).TextColor(tinyAccentStr)
					ui.Text(c, s.notice).FontSize(11).TextColor(tinyAccentStr)
				})
			}
			s.toolView(c)
			if s.settingsOpen {
				ui.Modal(c, &s.settingsOpen, func() { s.settingsModal(c) })
			}
		})
	})
}

// openSettings 统一入口：每次打开都按已保存的配置回填草稿，避免上次未生效的输入残留。
func (s *imageCompressSession) openSettings() {
	s.syncSettingDrafts()
	s.settingsOpen = true
}

func (s *imageCompressSession) header(c *ui.Context) {
	ui.Row(c).Height(84).Padding(0, 24).Gap(tinyGap).AlignItems(ui.Center).DragWindow().Children(func() {
		iconTile(c, "image-compress", 40, 10, tinyText, tinySurface)
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "图片压缩").FontSize(18).Bold().TextColor(tinyText)
			ui.Text(c, "批量压缩 PNG、JPEG 和 WebP，完成后可单张或打包下载").FontSize(11).TextColor(tinyTextMuted)
		})
		s.engineButton(c, "local", "本地压缩")
		s.engineButton(c, "tinypng", "TinyPNG 云")
		// 设置入口只显示齿轮图标；文字保留为无障碍名和 Tooltip，不占按钮宽度。
		settings := ui.Button(c.Key("tiny-settings"), "").Label("压缩设置").Tooltip("压缩设置").
			Size(tinyBtnHeight, tinyBtnHeight).Padding(0).Radius(tinyBtnRadius).Background(tinySurface).Border(1, tinyLine)
		settings.Children(func() {
			ui.Icon(c, icon("settings")).FontSize(16).TextColor(tinyTextMuted)
		})
		if settings.Clicked() {
			s.openSettings()
		}
	})
}

func (s *imageCompressSession) toolView(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		s.dropZone(c)
		s.queueView(c)
	})
}

func (s *imageCompressSession) engineButton(c *ui.Context, engine, label string) {
	button := ui.Button(c.Key("tiny-engine-"+engine), label).Height(tinyBtnHeight).Padding(0, tinyBtnPadX).Radius(tinyBtnRadius).
		Background(tinySurface).TextColor(tinyTextMuted).Border(1, tinyLine)
	if s.app.settings.ImageCompress.Engine == engine {
		button.Background(tinyAccentSoft).TextColor(tinyAccentStr).Border(1, ui.Hex("#67e8f9")).Bold()
	}
	if button.Clicked() {
		s.setEngine(engine)
	}
}

func (s *imageCompressSession) dropZone(c *ui.Context) {
	zone := ui.Column(c).Margin(tinyGap, 24, 0, 24).Height(140).Center().Gap(tinyGap).Radius(12).
		Background(tinySurface).Border(1, tinyLineStrong).BorderStyle(ui.BorderDashed)
	if files := zone.DroppedFiles(); files != nil {
		// DroppedFiles 会消费本次原生拖放事件；添加只入队，不会未经确认上传云端。
		s.addPaths(files)
	}
	if zone.FileDragOver() {
		zone.Border(2, tinyAccent).Background(tinyAccentSoft)
	}
	zone.Children(func() {
		ui.Box(c).Size(36, 36).Center().Radius(10).Background(tinyAccentSoft).Children(func() {
			ui.Icon(c, icon("upload")).FontSize(18).TextColor(tinyAccent)
		})
		ui.Row(c).Gap(tinyGap).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "拖入图片或点击选择").FontSize(14).Bold().TextColor(tinyText)
			ui.Text(c, "支持 PNG / JPEG / WebP，可批量选择").FontSize(11).TextColor(tinyTextMuted)
		})
		ui.Row(c).Gap(tinyGap).Children(func() {
			if tinyPrimaryBtn(c, "选择图片").Clicked() {
				s.chooseImages()
			}
			pending := 0
			for _, item := range s.items {
				if item.Status == tinyPending {
					pending++
				}
			}
			start := tinySecondaryBtn(c, "开始压缩").Disabled(pending == 0 || s.running)
			if start.Clicked() {
				s.startAll()
			}
		})
	})
}

func (s *imageCompressSession) queueView(c *ui.Context) {
	done, selected, pending := 0, 0, 0
	for _, item := range s.items {
		switch item.Status {
		case tinyDone:
			done++
			if item.Selected {
				selected++
			}
		case tinyPending:
			pending++
		}
	}
	ui.Column(c).Margin(tinyGap, 24, 24, 24).Grow(1).MinHeight(0).Radius(12).
		Background(tinySurface).Border(1, tinyLine).Children(func() {
		ui.Row(c).Height(48).Padding(0, 16).Gap(tinyGap).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(tinyLine).Children(func() {
			ui.Text(c, "压缩队列").FontSize(14).Bold().TextColor(tinyText)
			ui.Text(c, fmt.Sprintf("%d 项", len(s.items))).FontSize(11).TextColor(tinyTextMuted)
			ui.Spacer(c)
			if pending > 0 && !s.running {
				ui.Text(c, fmt.Sprintf("%d 张等待压缩", pending)).FontSize(11).TextColor(tinyAccentStr)
			}
			zipLabel := fmt.Sprintf("打包下载选中（%d）", selected)
			if s.zipping {
				zipLabel = "正在打包…"
			}
			if tinySecondaryBtn(c, zipLabel).Disabled(selected == 0 || s.zipping).Clicked() {
				s.downloadZIP()
			}
			clear := tinyGhostBtn(c, "清空队列").TextColor(tinyDanger).Disabled(len(s.items) == 0 || s.running)
			if clear.Clicked() {
				s.clearQueue()
			}
		})
		if len(s.items) == 0 {
			ui.Column(c).Grow(1).Center().Gap(tinyGap).Children(func() {
				ui.Text(c, "队列还是空的").FontSize(13).Bold().TextColor(tinyTextSoft)
				ui.Text(c, "选择的图片会先出现在这里，点击「开始压缩」后处理").FontSize(11).TextColor(tinyTextMuted)
			})
			return
		}
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(tinyGap, 16).Gap(tinyGap).Children(func() {
				// 一行两列：卡片等宽，奇数张时最后一行用等宽空盒占位，
				// 否则最后一张卡片会被拉伸成整行。
				for i := 0; i < len(s.items); i += 2 {
					ui.Row(c).Gap(tinyGap).AlignItems(ui.Stretch).Children(func() {
						s.itemCard(c, s.items[i])
						if i+1 < len(s.items) {
							s.itemCard(c, s.items[i+1])
						} else {
							// 占位块要带上和卡片相同的横向内边距，否则 Grow
							// 均分后卡片会比占位块宽出一个 padding。
							ui.Box(c).Grow(1).PaddingX(12)
						}
					})
				}
			})
		})
		ui.Row(c).Height(32).Padding(0, 16).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(tinyLine).Children(func() {
			ui.Text(c, fmt.Sprintf("已完成 %d/%d · 已选 %d", done, len(s.items), selected)).FontSize(10).TextColor(tinyTextMuted)
		})
	})
}

// tinyIconBtn 是队列卡片上的图标按钮：文字只作为无障碍名和 Tooltip，
// 不占宽度——一张卡片里最多三个操作，文字按钮会把文件名挤成省略号。
func tinyIconBtn(c *ui.Context, key, iconName, label string, primary bool) ui.Element {
	btn := ui.Button(c.Key(key), "").Label(label).Tooltip(label).
		Size(tinyBtnHeight, tinyBtnHeight).Padding(0).Radius(tinyBtnRadius)
	color := tinyTextMuted
	if primary {
		btn = btn.Background(tinyAccent).Border(1, tinyAccent)
		color = tinySurface
	} else {
		btn = btn.Background(tinySurface).Border(1, tinyLine)
	}
	h := btn
	h.Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(15).TextColor(color)
	})
	return h
}

// itemCard 是队列里的一张卡片：勾选、缩略图、文案和操作按钮排在同一行，
// 按钮不再单独占一行，卡片高度只由内容决定。
func (s *imageCompressSession) itemCard(c *ui.Context, item *imageCompressItem) {
	ui.Row(c.Key(fmt.Sprintf("tiny-item-%d", item.ID))).Grow(1).MinWidth(0).
		Padding(tinyGap, 12).Radius(8).Background(tinySoft).Border(1, tinyLine).
		Gap(tinyGap).AlignItems(ui.Center).Children(func() {
		if item.Status == tinyDone {
			ui.Checkbox(c, &item.Selected, "")
		}
		if item.Thumb != nil {
			ui.Image(c, item.Thumb).Size(40, 40)
		} else {
			ui.Box(c).Size(40, 40).Center().Radius(8).Background(tinySurface).Border(1, tinyLine).Children(func() {
				if item.Status == tinyProcessing {
					ui.Spinner(c)
				} else {
					ui.Icon(c, icon("file")).FontSize(16).TextColor(tinyTextMuted)
				}
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, filepath.Base(item.SourcePath)).FontSize(12).Bold().TextColor(tinyText).MaxLines(1)
			detail := fmt.Sprintf("%s · %s", strings.ToUpper(item.Format), formatByteSize(item.OriginalSize))
			if item.Status == tinyDone {
				percent := compressionPercent(item.OriginalSize, len(item.Data))
				detail = fmt.Sprintf("%s → %s · 减少 %d%%", formatByteSize(item.OriginalSize), formatByteSize(int64(len(item.Data))), percent)
				if item.Width > 0 && item.Height > 0 {
					detail += fmt.Sprintf(" · %d×%d", item.Width, item.Height)
				}
			}
			ui.Text(c, detail).FontSize(11).TextColor(tinyTextMuted).MaxLines(1)
			// 每种状态都保留一行状态文字，卡片高度才不会因为压缩完成而缩一截。
			switch item.Status {
			case tinyPending:
				ui.Text(c, "等待压缩").FontSize(10).TextColor(tinyTextMuted)
			case tinyDone:
				ui.Text(c, "已完成").FontSize(10).TextColor(tinySuccess)
			case tinyProcessing:
				ui.Text(c, "正在压缩…").FontSize(10).TextColor(tinyAccentStr)
			case tinyError:
				ui.Text(c, item.Err).FontSize(10).TextColor(tinyDanger).MaxLines(2)
			}
		})
		ui.Row(c).Gap(tinyGap).AlignItems(ui.Center).Children(func() {
			k := func(name string) string { return fmt.Sprintf("tiny-item-%d-%s", item.ID, name) }
			if item.Saving {
				ui.Text(c, "保存中…").FontSize(10).TextColor(tinyTextMuted)
				return
			}
			switch item.Status {
			case tinyPending:
				if tinyIconBtn(c, k("run"), "zap", "压缩", true).Disabled(s.running).Clicked() {
					s.startOne(item.ID)
				}
			case tinyDone:
				if tinyIconBtn(c, k("download"), "download", "下载", true).Clicked() {
					s.download(item.ID, false)
				}
				if tinyIconBtn(c, k("overwrite"), "replace", "覆盖原图", false).Clicked() {
					s.download(item.ID, true)
				}
			case tinyError:
				if tinyIconBtn(c, k("retry"), "refresh", "重试", false).Disabled(s.running).Clicked() {
					s.retry(item.ID)
				}
			}
			if item.Status != tinyProcessing && tinyIconBtn(c, k("remove"), "trash", "移除", false).Clicked() {
				s.remove(item.ID)
			}
		})
	})
}

// settingsModal 是压缩设置弹框。关闭只依赖右上角图标和点击遮罩/Esc，
// 不再单独放「关闭」按钮——系统窗口本身已有关闭入口。
func (s *imageCompressSession) settingsModal(c *ui.Context) {
	ui.Column(c).Width(620).Gap(16).Padding(4).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "压缩设置").FontSize(21).Bold().TextColor(tinyText)
				ui.Text(c, "参数保存在本机，并应用于之后开始的任务").FontSize(11).TextColor(tinyTextMuted)
			})
			closeBtn := ui.Button(c.Key("tiny-settings-close"), "").Label("关闭").Size(tinyBtnHeight, tinyBtnHeight).Center().
				Background(ui.Transparent).Radius(8)
			closeBtn.Children(func() {
				ui.Icon(c, icon("close")).FontSize(15).TextColor(tinyTextMuted)
			})
			if closeBtn.Clicked() {
				s.settingsOpen = false
			}
		})
		ui.Scroll(c).MaxHeight(520).Children(func() { s.settingsView(c) })
	})
}

// settingsView 是压缩设置弹框的全部内容，本地参数、Key 都放在同一张卡片里，
// 避免为一段参数再拆一张子卡片。
func (s *imageCompressSession) settingsView(c *ui.Context) {
	ui.Column(c).Padding(16).Gap(16).Radius(12).Background(tinySurface).Border(1, tinyLine).Children(func() {
		ui.Row(c).Gap(tinyGap).Children(func() {
			s.engineButton(c, "local", "本地压缩（默认）")
			s.engineButton(c, "tinypng", "TinyPNG 云")
		})
		ui.Text(c, "本地模式完全离线；TinyPNG 模式会将图片上传到第三方服务。").FontSize(11).TextColor(tinyTextMuted)
		if s.app.settings.ImageCompress.Engine == "tinypng" {
			s.tinyPNGSettings(c)
			return
		}
		ui.Row(c).Gap(16).Children(func() {
			s.numberField(c, "PNG 优化级别（0–6）", &s.pngLevelText, "3", "0 不优化，6 压缩率最高最慢", "png")
			s.numberField(c, "JPEG 质量（1–100）", &s.jpegQualityText, "80", "数值越高画质越好，压缩后越小", "jpeg")
		})
		ui.Row(c).Gap(16).Children(func() {
			s.numberField(c, "WebP 质量（1–100）", &s.webpQualityText, "80", "数值越高画质越好，压缩后越小", "webp")
			s.numberField(c, "最长边像素（0 不限制）", &s.maxEdgeText, "0", "限制图片最长边，同时缩放并压缩", "maxEdge")
		})
	})
}

func (s *imageCompressSession) numberField(c *ui.Context, label string, value *string, placeholder, hint, field string) {
	ui.Column(c).Grow(1).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(tinyTextSoft)
		if input := ui.TextInput(c, value).Placeholder(placeholder).Height(tinyBtnHeight); input.Changed() {
			s.saveNumberSetting(*value, field)
		}
		if hint != "" {
			ui.Text(c, hint).FontSize(10).TextColor(tinyTextMuted)
		}
	})
}

// tinyPNGSettings 只维护一把 Key：云端模式的 Key 是账号级凭据，多把轮换对个人使用没有意义。
func (s *imageCompressSession) tinyPNGSettings(c *ui.Context) {
	keys := s.app.settings.ImageCompress.TinyPNGKeys
	if len(keys) == 0 {
		ui.Text(c, "尚未配置 Key，云端模式无法使用。").FontSize(11).TextColor(tinyDanger)
	} else {
		ui.Row(c).Padding(tinyGap, 12).Radius(8).Background(tinySoft).
			Gap(tinyGap).AlignItems(ui.Center).Children(func() {
			ui.Text(c, maskTinyPNGKey(keys[0].Key)).FontSize(12).TextColor(tinyText).Grow(1)
			ui.Text(c, fmt.Sprintf("本月已用 %d/500", keys[0].Usage)).FontSize(11).TextColor(tinyTextMuted)
			if tinyGhostBtn(c, "移除").TextColor(tinyDanger).Clicked() {
				s.app.settings.ImageCompress.TinyPNGKeys = nil
				_ = s.app.persist()
			}
		})
	}
	ui.Row(c).Gap(tinyGap).Children(func() {
		ui.TextInput(c, &s.newKey).Placeholder("粘贴 TinyPNG API Key").Height(tinyBtnHeight).Grow(1)
		if tinyPrimaryBtn(c, "保存 Key").Clicked() {
			s.saveTinyPNGKey()
		}
	})
}

// thumbnailMaxEdge 是队列缩略图的最长边：卡片上只显示 40 点，按 2 倍屏取 96，
// 再大只会让批量选图时多占内存。
const thumbnailMaxEdge = 96

// thumbnailFromBytes 把图片缩成小图再编码成位图：直接给 ui.DecodeBitmap 原图
// 会让队列里每张图都保留一份整图纹理，一次选几十张时首帧和内存都吃不消。
func thumbnailFromBytes(data []byte, format string) (*ui.Bitmap, error) {
	img, err := decodeCompressImage(data, format)
	if err != nil {
		return nil, err
	}
	width, height := scaledSize(img.Bounds().Dx(), img.Bounds().Dy(), thumbnailMaxEdge)
	small := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Over, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		return nil, err
	}
	return ui.DecodeBitmap(buf.Bytes())
}

// loadThumbnails 在后台串行给新加入的图片补缩略图：读盘解码都比一帧慢，
// 并发解码反而会拖住主线程；缩略图失败只影响观感，不报错。
func (s *imageCompressSession) loadThumbnails(items []*imageCompressItem) {
	win := s.win
	if win == nil || len(items) == 0 {
		return
	}
	for _, item := range items {
		if s.ctx.Err() != nil {
			return
		}
		id, source, format := item.ID, item.SourcePath, item.Format
		data, err := os.ReadFile(source)
		if err != nil {
			continue
		}
		thumb, err := thumbnailFromBytes(data, format)
		if err != nil || thumb == nil {
			continue
		}
		win.Update(func() {
			// 压缩完成时可能已经写入压缩后的缩略图，后者优先。
			if target := s.item(id); target != nil && target.Thumb == nil {
				target.Thumb = thumb
			}
		})
	}
}

func formatByteSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size) / 1024
	unit := "KB"
	if value >= 1024 {
		value /= 1024
		unit = "MB"
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func compressionPercent(original int64, compressed int) int {
	if original <= 0 || compressed >= int(original) {
		return 0
	}
	return int((1 - float64(compressed)/float64(original)) * 100)
}
