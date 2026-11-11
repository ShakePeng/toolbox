package main

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pinWindow 是一张贴在桌面上的截图：无边框、置顶、可拖动的小窗口。
// 第一版只做「贴出来 + 拖动 + 关闭」，缩放、透明度等留到以后。
type pinWindow struct {
	win *mygo.Window
	bmp *ui.Bitmap
}

// pinBounds 把截图覆盖层里的选区换算成贴图窗口在屏幕上的位置和尺寸。
// 裁剪时四周各内缩 1 点（去掉选区边框），贴图窗口也跟着内缩，保证
// 画面不会被边框多算一圈。
func pinBounds(area mygo.Rectangle, sel ui.Rect) (x, y, w, h int) {
	const inset = 1.0
	x = int(math.Round(float64(area.X) + float64(sel.X) + inset))
	y = int(math.Round(float64(area.Y) + float64(sel.Y) + inset))
	w = int(math.Round(float64(sel.W - 2*inset)))
	h = int(math.Round(float64(sel.H - 2*inset)))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return
}

// pinImage 在屏幕坐标 (x, y) 处贴出一张宽 w、高 h（逻辑点）的截图。
// pngData 是已经带标注、裁好的图片；贴图失败时返回 nil。
func (a *app) pinImage(pngData []byte, x, y, w, h int) *pinWindow {
	bmp, err := ui.DecodeBitmap(pngData)
	if err != nil {
		a.toast("贴图失败：" + err.Error())
		return nil
	}
	p := &pinWindow{bmp: bmp}
	p.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "贴图",
		Width:             w,
		Height:            h,
		Hidden:            true,
		Frameless:         true,
		Panel:             true,
		AlwaysOnTop:       true,
		SkipTaskbar:       true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		Content:           ui.View(p.view),
	})
	p.win.SetVisibleOnAllWorkspaces(true)
	p.win.OnClosed(func() {
		a.removePin(p)
	})
	// 等隐藏窗口画完第一帧再定位显示，避免贴图位置闪一下空白。
	// ShowInactive：贴图后不抢走用户当前应用的焦点，方便继续操作。
	p.win.OnReadyToShow(func() {
		p.win.SetPosition(x, y)
		p.win.ShowInactive()
	})
	a.pins = append(a.pins, p)
	return p
}

func (a *app) removePin(p *pinWindow) {
	for i, q := range a.pins {
		if q == p {
			a.pins = append(a.pins[:i], a.pins[i+1:]...)
			return
		}
	}
}

func (p *pinWindow) close() {
	if p.win != nil {
		p.win.Close()
	}
}

func (p *pinWindow) view(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEscape) {
		p.close()
		return
	}
	root := ui.Box(c).Fill().DragWindow().Cursor(ui.CursorGrab).
		HandleInput(func(ev ui.InputEvent) bool {
			// 右键直接关闭，和截图覆盖层里的右键退出保持一致。
			if ev.Kind == ui.InputPointerDown && ev.Button != 0 {
				p.close()
				return true
			}
			return false
		}).
		Draw(func(pt *ui.Painter, r ui.Rect) {
			if p.bmp != nil {
				pt.Image(p.bmp, r, ui.FillBox)
			}
		})
	hovered := root.Hovered()
	root.Children(func() {
		if !hovered {
			return
		}
		btn := ui.Button(c.Key("pin-close"), "").Label("关闭").
			Absolute().Right(6).Top(6).Size(22, 22).Radius(11).Padding(0).Center().
			Background(ui.RGBA(0, 0, 0, 0.55))
		btn.Children(func() {
			ui.Icon(c, icon("close")).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.95))
		})
		if btn.Clicked() {
			p.close()
		}
	})
}
