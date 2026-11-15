package main

import (
	"log"
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// 截图时「悬停选中应用窗口」的跨平台部分。
//
// 平台文件只负责回答「当前屏幕上有哪些正常窗口」（屏幕 DIP 坐标、z 序从前到后），
// 尺寸下限、与当前屏幕求交、坐标换算和命中判定都集中在这里，
// 这样 macOS / Windows 的行为一致，也能脱离窗口服务器做纯单测。
const (
	// pickDragSlop 是「单击选窗」与「拖拽框选」的分界：按下后移动超过这个
	// 距离，就认为用户想自由框选，放弃窗口候选。
	pickDragSlop = 3
	// minPickWidth / minPickHeight 过滤提示框、浮动小面板这类不适合整块截取的窗口。
	minPickWidth  = 32
	minPickHeight = 32
	// minVisible 是裁剪到当前屏幕后仍可点选的最小尺寸。
	minVisible = 3
)

// listCaptureWindows 枚举当前屏幕上的应用窗口，并兜底平台绑定可能出现的
// panic：宁可让悬停选窗静默失效，也不能在 startCapture 里崩——那里不在
// recoverAndClose 的保护范围内（覆盖层还没建立）。
func listCaptureWindows(disp mygo.Display) (out []mygo.Rectangle) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("枚举截图候选窗口失败：%v", r)
			out = nil
		}
	}()
	return listCaptureWindowsPlatform(disp)
}

// clipWindowRect 把屏幕 DIP 矩形裁剪到截图区域 area，并转成覆盖层局部坐标。
// 全程用整数求交：屏幕与覆盖层都是整点坐标，转 float32 之前不会引入误差。
func clipWindowRect(area mygo.Rectangle, w mygo.Rectangle) (ui.Rect, bool) {
	x0 := max(w.X, area.X)
	y0 := max(w.Y, area.Y)
	x1 := min(w.X+w.Width, area.X+area.Width)
	y1 := min(w.Y+w.Height, area.Y+area.Height)
	if x1-x0 < minVisible || y1-y0 < minVisible {
		return ui.Rect{}, false
	}
	return ui.Rect{
		X: float32(x0 - area.X),
		Y: float32(y0 - area.Y),
		W: float32(x1 - x0),
		H: float32(y1 - y0),
	}, true
}

// localWindowRects 过滤过小窗口并裁剪到当前屏幕，保持输入的 z 序。
// 只考虑与当前截图显示器相交的窗口：多显示器下别把另一块屏的窗口带进来。
func localWindowRects(area mygo.Rectangle, wins []mygo.Rectangle) []ui.Rect {
	out := make([]ui.Rect, 0, len(wins))
	for _, w := range wins {
		if w.Width < minPickWidth || w.Height < minPickHeight {
			continue
		}
		if r, ok := clipWindowRect(area, w); ok {
			out = append(out, r)
		}
	}
	return out
}

// windowAt 返回光标下的窗口下标。窗口按 z 序从前到后排列，
// 取第一个命中的就是用户实际看到的那个窗口；不做「面积最小」之类的重排。
func windowAt(p ui.Point, wins []ui.Rect) int {
	for i, r := range wins {
		if pointInRect(p, r) {
			return i
		}
	}
	return -1
}

// movedBeyond 判断两次指针位置是否已经超过阈值。
func movedBeyond(a, b ui.Point, slop float32) bool {
	return math.Abs(float64(a.X-b.X)) > float64(slop) ||
		math.Abs(float64(a.Y-b.Y)) > float64(slop)
}

// hoverRect 返回当前应当高亮的窗口矩形。有选区时一律没有悬停：
// 悬停只是「即将选择」的预览，不能让两套边框同时出现。
// 绘制工具（选中过、又清空选区时可能残留）同样不显示：此时按下不会走选窗分支。
func (s *captureSession) hoverRect() (ui.Rect, bool) {
	if s.tool != "select" || s.hasSel || s.hover < 0 || s.hover >= len(s.windows) {
		return ui.Rect{}, false
	}
	return s.windows[s.hover], true
}

// paintHover 给悬停窗口描边。刻意不复用 paintSel：那里会画 8 个手柄和
// 尺寸标签，那是「已经选中」的视觉语言，会让用户误以为选区已建立。
func (s *captureSession) paintHover(p *ui.Painter) {
	r, ok := s.hoverRect()
	if !ok {
		return
	}
	p.Stroke(r, ui.RGBA(255, 255, 255, 0.95), 0, 1)
}

// commitPick 在抬起时确认窗口选择。只有「按下到抬起几乎没动」才算单击；
// 移动超过阈值说明用户想框选，按普通框选取尾。
func (s *captureSession) commitPick(p ui.Point) {
	defer func() { s.pickWin = -1 }()

	if movedBeyond(s.press, p, pickDragSlop) {
		// 系统没有派发中间的 Move（例如按下后直接把指针甩到远处），
		// 这里补一次与 dsNewSel 抬起一致的收尾，避免用户松手后什么都没发生。
		s.sel = rectFromPoints(s.press, p)
		s.clampSel()
		s.hasSel = s.sel.W >= minVisible && s.sel.H >= minVisible
		s.hover = windowAt(p, s.windows)
		return
	}
	if s.pickWin < 0 || s.pickWin >= len(s.windows) {
		s.hover = windowAt(p, s.windows)
		return
	}
	// 直接用按下时记下的窗口，抬起时不再重新命中：按下瞬间的高亮已经告诉
	// 用户选的是哪个窗口，重新命中可能因取整误差跳到相邻窗口。
	r := s.windows[s.pickWin]
	if r.W < minVisible || r.H < minVisible {
		s.hover = windowAt(p, s.windows)
		return
	}
	s.hasSel = true
	s.sel = r
	s.hover = -1
}
