package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type captureAction int

const (
	actCopy captureAction = iota
	actSave
)

// 拖拽状态
const (
	dsNone = iota
	dsNewSel
	dsResize
	dsMoveSel
	dsDraw
	dsMoveMark
)

type captureSession struct {
	app     *app
	win     *mygo.Window
	display mygo.Display

	bitmap *ui.Bitmap  // 冻结画面（绘制用）
	srcImg image.Image // 冻结画面（像素采样用）
	W, H   float32     // 覆盖层逻辑尺寸（点）
	scale  float32     // 每点的物理像素数

	restoreMain bool // 结束后是否恢复主窗口

	hasSel bool
	sel    ui.Rect

	tool        string
	color       ui.Color
	strokeWidth float32

	marks    []mark
	selected int

	undoStack [][]mark
	redoStack [][]mark
	pre       []mark // 移动标注前的快照

	// 拖拽
	drag       int
	press      ui.Point
	origSel    ui.Rect
	dragHandle int
	draft      mark

	// 文字编辑
	editingText bool
	textValue   string
	textPos     ui.Point
	textIndex   int
}

// ---------------- 启动 ----------------

func (a *app) startCapture() {
	if a.capture != nil {
		a.capture.win.Focus()
		return
	}
	pt := mygo.Screen.CursorScreenPoint()
	disp := mygo.Screen.DisplayNearestPoint(pt)
	wa := disp.WorkArea

	// 先确认屏幕录制权限；没有就先申请并提示，避免抓到纯壁纸造成“窗口消失”的错觉。
	if !screenRecordingGranted() {
		requestScreenRecording()
		a.toast("请在“系统设置 → 隐私与安全性 → 屏幕录制”勾选本应用后重试；首次授权后需重启应用")
		return
	}

	restore := a.win != nil && a.win.IsVisible()
	if restore {
		a.win.Hide()
	}

	pngData, img, err := captureRegion(wa)
	if err != nil {
		if restore {
			a.win.Show()
		}
		a.toast("截图失败：" + err.Error())
		return
	}
	a.openCaptureOverlay(disp, wa, pngData, img, restore)
}

// captureRegion 用系统 screencapture 抓取一块屏幕区域到 PNG，并解码像素。
func captureRegion(r mygo.Rectangle) ([]byte, image.Image, error) {
	f, err := os.CreateTemp("", "toolbox-cap-*.png")
	if err != nil {
		return nil, nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	rect := fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height)
	out, err := exec.Command("/usr/sbin/screencapture", "-x", "-t", "png", "-R", rect, path).CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("%v %s", err, bytes.TrimSpace(out))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("解码截图失败：%w", err)
	}
	return data, img, nil
}

func (a *app) openCaptureOverlay(disp mygo.Display, wa mygo.Rectangle, pngData []byte, img image.Image, restore bool) {
	bitmap, err := ui.DecodeBitmap(pngData)
	if err != nil {
		if restore {
			a.win.Show()
		}
		a.toast("加载截图失败：" + err.Error())
		return
	}
	scale := float32(disp.ScaleFactor)
	if scale <= 0 {
		scale = 1
	}
	s := &captureSession{
		app:         a,
		display:     disp,
		bitmap:      bitmap,
		srcImg:      img,
		W:           float32(wa.Width),
		H:           float32(wa.Height),
		scale:       scale,
		restoreMain: restore,
		tool:        "select",
		color:       ui.RGBA(244, 63, 63, 1),
		strokeWidth: 3,
		selected:    -1,
	}
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "截图",
		X:                 wa.X,
		Y:                 wa.Y,
		Width:             wa.Width,
		Height:            wa.Height,
		Frameless:         true,
		Panel:             true,
		AlwaysOnTop:       true,
		SkipTaskbar:       true,
		DisableResize:     true,
		DisableMove:       true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		Content:           ui.View(s.view),
	})
	s.win.SetVisibleOnAllWorkspaces(true)
	s.win.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		s.cancel()
	})
	a.capture = s
	s.win.Show()
	s.win.Focus()
}

// ---------------- 视图 ----------------

func (s *captureSession) view(c *ui.Context) {
	// 键盘
	if s.editingText {
		if c.Shortcut(0, ui.KeyEscape) {
			s.editingText = false
		}
	} else {
		if c.Shortcut(0, ui.KeyEscape) {
			s.cancel()
		}
		if c.Shortcut(0, ui.KeyEnter) {
			s.complete(actCopy)
		}
		if c.Shortcut(ui.Cmd, ui.KeyC) {
			s.complete(actCopy)
		}
		if c.Shortcut(ui.Cmd, ui.KeyS) {
			s.complete(actSave)
		}
		if c.Shortcut(0, ui.KeyDelete) || c.Shortcut(0, ui.KeyBackspace) {
			s.deleteSelected()
		}
		if c.Shortcut(ui.Shift, ui.KeyZ) || c.Shortcut(ui.Cmd|ui.Shift, ui.KeyZ) {
			s.redo()
		} else if c.Shortcut(ui.Cmd, ui.KeyZ) {
			s.undo()
		}
	}

	ui.Themed(c, ui.DarkTheme()).Fill().Children(func() {
		ui.Box(c).Fill().HandleInput(s.onInput).Draw(func(p *ui.Painter, r ui.Rect) {
			s.paint(p)
		})
		// 只有首次拉框时收起工具栏；绘制/移动过程中保持显示，
		// 免得工具栏闪一下又出现（mygo 按下后由被按元素独占 move/up，不会抢事件）。
		if s.hasSel && s.drag != dsNewSel {
			s.toolbar(c)
		}
		s.textEditor(c)
	})
}

func (s *captureSession) paint(p *ui.Painter) {
	full := ui.Rect{X: 0, Y: 0, W: s.W, H: s.H}
	if s.bitmap != nil {
		p.Image(s.bitmap, full, ui.FillBox)
	}
	// 整体压暗蒙层（有无选区都先压暗，明确这是截图态）
	p.Fill(full, ui.RGBA(0, 0, 0, 0.42), 0)
	// 选区内部还原成清晰画面
	if s.hasSel {
		if sel := normRect(s.sel); sel.W > 0 && sel.H > 0 {
			p.Clip(sel, 0, func() {
				if s.bitmap != nil {
					p.Image(s.bitmap, full, ui.FillBox)
				}
			})
		}
	}
	// 标注统一裁剪到选区内：工具只在选区内生效，笔迹也不该漏到选区外
	// （马赛克笔刷是会外扩的方块，不裁剪就会溢出去）。
	drawMarks := func() {
		for _, m := range s.marks {
			drawMark(p, m, s.srcImg, s.scale)
		}
		if s.drag == dsDraw {
			drawMark(p, s.draft, s.srcImg, s.scale)
		}
	}
	if s.hasSel {
		if sel := normRect(s.sel); sel.W > 0 && sel.H > 0 {
			p.Clip(sel, 0, drawMarks)
		}
	} else {
		drawMarks()
	}
	s.paintSel(p)
	if !s.hasSel {
		s.paintHint(p)
	}
}

// paintHint 在尚未框选时给出操作提示。
func (s *captureSession) paintHint(p *ui.Painter) {
	const txt = "拖拽选择截图区域 · 双击全屏 · Esc 取消"
	w := float32(380)
	x := (s.W - w) / 2
	y := float32(44)
	p.Fill(ui.Rect{X: x, Y: y, W: w, H: 32}, ui.RGBA(0, 0, 0, 0.62), 8)
	p.Text(x+18, y+21, txt, 13, ui.RGBA(255, 255, 255, 0.95))
}

func (s *captureSession) paintSel(p *ui.Painter) {
	if !s.hasSel {
		return
	}
	sel := normRect(s.sel)
	p.Stroke(sel, ui.RGBA(255, 255, 255, 0.95), 0, 1)
	white := ui.RGBA(255, 255, 255, 1)
	for i := 0; i < 8; i++ {
		c := s.handleCenter(sel, i)
		p.Fill(ui.Rect{X: c.X - 4, Y: c.Y - 4, W: 8, H: 8}, white, 1)
		p.Stroke(ui.Rect{X: c.X - 4, Y: c.Y - 4, W: 8, H: 8}, ui.RGBA(0, 0, 0, 0.5), 1, 1)
	}
	label := fmt.Sprintf("%d × %d", int(sel.W), int(sel.H))
	ly := sel.Y - 20
	if ly < 4 {
		ly = sel.Y + 6
	}
	lx := clamp(sel.X, 4, s.W-90)
	p.Fill(ui.Rect{X: lx - 4, Y: ly - 2, W: 84, H: 18}, ui.RGBA(0, 0, 0, 0.6), 4)
	p.Text(lx, ly+12, label, 12, ui.RGBA(255, 255, 255, 1))
}

// handleDir 返回第 i 个手柄向外的单位方向。
func handleDir(i int) ui.Point {
	dirs := [8]ui.Point{
		{X: -1, Y: -1}, {X: 0, Y: -1}, {X: 1, Y: -1}, {X: 1, Y: 0},
		{X: 1, Y: 1}, {X: 0, Y: 1}, {X: -1, Y: 1}, {X: -1, Y: 0},
	}
	return dirs[i&7]
}

// handleBase 返回第 i 个手柄在选区上的锚点。
func handleBase(sel ui.Rect, i int) ui.Point {
	cx, cy := sel.X+sel.W/2, sel.Y+sel.H/2
	bases := [8]ui.Point{
		{X: sel.X, Y: sel.Y}, {X: cx, Y: sel.Y}, {X: sel.X + sel.W, Y: sel.Y}, {X: sel.X + sel.W, Y: cy},
		{X: sel.X + sel.W, Y: sel.Y + sel.H}, {X: cx, Y: sel.Y + sel.H}, {X: sel.X, Y: sel.Y + sel.H}, {X: sel.X, Y: cy},
	}
	pt := bases[i&7]
	return pt
}

// handleCenter 返回手柄绘制/命中的中心（向选区外侧偏移）。
func (s *captureSession) handleCenter(sel ui.Rect, i int) ui.Point {
	b := handleBase(sel, i)
	d := handleDir(i)
	const off = 7
	return ui.Point{X: b.X + d.X*off, Y: b.Y + d.Y*off}
}

func (s *captureSession) hitHandle(p ui.Point) int {
	if !s.hasSel {
		return -1
	}
	sel := normRect(s.sel)
	for i := 0; i < 8; i++ {
		c := s.handleCenter(sel, i)
		if math.Abs(float64(p.X-c.X)) <= 8 && math.Abs(float64(p.Y-c.Y)) <= 8 {
			return i
		}
	}
	return -1
}

func (s *captureSession) clampSel() {
	r := normRect(s.sel)
	r.X = clamp(r.X, 0, s.W)
	r.Y = clamp(r.Y, 0, s.H)
	r.W = clamp(r.W, 0, s.W-r.X)
	r.H = clamp(r.H, 0, s.H-r.Y)
	s.sel = r
}

// ---------------- 输入 ----------------

func (s *captureSession) onInput(ev ui.InputEvent) bool {
	p := ui.Point{X: ev.X, Y: ev.Y}
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		s.press = p
		s.origSel = s.sel
		// 「选择」工具：框选、移动/调整选区、选标注并移动
		if s.tool == "select" {
			if ev.Clicks >= 2 && (!s.hasSel || !pointInRect(p, normRect(s.sel))) {
				s.sel = ui.Rect{X: 0, Y: 0, W: s.W, H: s.H}
				s.hasSel = true
				if ev.Mods == 0 {
					s.complete(actCopy)
				} else {
					s.complete(actSave)
				}
				return true
			}
			if !s.hasSel {
				s.drag = dsNewSel
				s.hasSel = true
				s.sel = ui.Rect{X: p.X, Y: p.Y}
				return true
			}
			if h := s.hitHandle(p); h >= 0 {
				s.drag = dsResize
				s.dragHandle = h
				return true
			}
			if pointInRect(p, normRect(s.sel)) {
				if idx := s.markAt(p); idx >= 0 {
					s.selected = idx
					s.pre = cloneMarks(s.marks)
					s.drag = dsMoveMark
					return true
				}
				s.drag = dsMoveSel
				return true
			}
			s.drag = dsNewSel
			s.hasSel = true
			s.sel = ui.Rect{X: p.X, Y: p.Y}
			return true
		}
		// 绘制工具：只在选区内绘制；选区外无任何反应（要重新框选请切回选择工具）
		if !s.hasSel || !pointInRect(p, normRect(s.sel)) {
			return true
		}
		if s.tool == "text" {
			s.beginText(p)
			return true
		}
		s.drag = dsDraw
		s.draft = s.newMark(p)
		return true

	case ui.InputPointerMove:
		switch s.drag {
		case dsNewSel:
			s.sel = rectFromPoints(s.press, p)
			s.clampSel()
		case dsResize:
			s.resizeSel(p)
		case dsMoveSel:
			s.sel = ui.Rect{X: s.origSel.X + (p.X - s.press.X), Y: s.origSel.Y + (p.Y - s.press.Y), W: s.origSel.W, H: s.origSel.H}
			s.clampSel()
		case dsMoveMark:
			if s.selected >= 0 && s.selected < len(s.marks) {
				nm := s.pre[s.selected]
				translateMark(&nm, p.X-s.press.X, p.Y-s.press.Y)
				s.marks[s.selected] = nm
			}
		case dsDraw:
			s.updateDraft(p)
		}
		if s.win != nil {
			s.win.Invalidate()
		}
		return true

	case ui.InputPointerUp:
		switch s.drag {
		case dsNewSel:
			s.sel = rectFromPoints(s.press, p)
			s.clampSel()
			if s.sel.W < 3 || s.sel.H < 3 {
				s.hasSel = false
			}
		case dsDraw:
			s.updateDraft(p)
			if s.markValid(s.draft) {
				s.pushUndo()
				s.marks = append(s.marks, s.draft)
				s.selected = len(s.marks) - 1
			}
		case dsMoveMark:
			if s.selected >= 0 {
				s.undoStack = append(s.undoStack, s.pre)
				s.redoStack = nil
			}
		}
		s.drag = dsNone
		if s.win != nil {
			s.win.Invalidate()
		}
		return true
	}
	return false
}

func (s *captureSession) resizeSel(p ui.Point) {
	sel := normRect(s.origSel)
	left, right, top, bottom := sel.X, sel.X+sel.W, sel.Y, sel.Y+sel.H
	h := s.dragHandle
	switch h {
	case 0, 6, 7:
		left = p.X
	}
	switch h {
	case 2, 3, 4:
		right = p.X
	}
	switch h {
	case 0, 1, 2:
		top = p.Y
	}
	switch h {
	case 4, 5, 6:
		bottom = p.Y
	}
	s.sel = normRect(ui.Rect{X: left, Y: top, W: right - left, H: bottom - top})
	s.clampSel()
}

func (s *captureSession) newMark(p ui.Point) mark {
	m := mark{color: s.color, width: s.strokeWidth}
	switch s.tool {

	case "rect":
		m.kind = mkRect
	case "ellipse":
		m.kind = mkEllipse
	case "line":
		m.kind = mkLine
		m.a, m.b = p, p
	case "arrow":
		m.kind = mkArrow
		m.a, m.b = p, p
	case "pen":
		m.kind = mkPen
		m.pts = []ui.Point{p}
	case "mosaic":
		m.kind = mkMosaic
		m.pts = []ui.Point{p}
	}
	m.rect = ui.Rect{X: p.X, Y: p.Y}
	return m
}

func (s *captureSession) updateDraft(p ui.Point) {
	switch s.draft.kind {
	case mkLine, mkArrow:
		s.draft.b = p
	case mkPen, mkMosaic:
		s.draft.pts = append(s.draft.pts, p)
	default:
		s.draft.rect = rectFromPoints(s.press, p)
	}
}

func (s *captureSession) markValid(m mark) bool {
	switch m.kind {
	case mkLine, mkArrow:
		return math.Hypot(float64(m.b.X-m.a.X), float64(m.b.Y-m.a.Y)) > 3
	case mkPen:
		return len(m.pts) >= 2
	case mkMosaic:
		return len(m.pts) >= 1
	default:
		r := normRect(m.rect)
		return r.W > 2 && r.H > 2
	}
}

func (s *captureSession) markAt(p ui.Point) int {
	for i := len(s.marks) - 1; i >= 0; i-- {
		if markHit(p, s.marks[i], 6+s.marks[i].width/2) {
			return i
		}
	}
	return -1
}

// ---------------- 文字 ----------------

func (s *captureSession) beginText(p ui.Point) {
	s.editingText = true
	s.textValue = ""
	s.textPos = p
	s.textIndex = -1
}

func (s *captureSession) commitText() {
	if s.textValue == "" {
		s.editingText = false
		return
	}
	size := 14 + s.strokeWidth*2
	m := mark{kind: mkText, rect: ui.Rect{X: s.textPos.X, Y: s.textPos.Y}, text: s.textValue, color: s.color, size: size, width: s.strokeWidth}
	s.pushUndo()
	s.marks = append(s.marks, m)
	s.selected = len(s.marks) - 1
	s.editingText = false
	s.textValue = ""
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *captureSession) textEditor(c *ui.Context) {
	if !s.editingText {
		return
	}
	ui.Row(c).Absolute().Left(clamp(s.textPos.X, 4, s.W-260)).Top(clamp(s.textPos.Y, 4, s.H-40)).
		Width(250).Children(func() {
		if ui.TextInput(c, &s.textValue).AutoFocus().Label("文字").Submitted() {
			s.commitText()
		}
	})
}

// ---------------- 历史 ----------------

func (s *captureSession) pushUndo() {
	s.undoStack = append(s.undoStack, cloneMarks(s.marks))
	s.redoStack = nil
}

func (s *captureSession) undoStep() {
	if len(s.undoStack) == 0 {
		return
	}
	s.redoStack = append(s.redoStack, cloneMarks(s.marks))
	s.marks = s.undoStack[len(s.undoStack)-1]
	s.undoStack = s.undoStack[:len(s.undoStack)-1]
	s.selected = -1
}

func (s *captureSession) redoStep() {
	if len(s.redoStack) == 0 {
		return
	}
	s.undoStack = append(s.undoStack, cloneMarks(s.marks))
	s.marks = s.redoStack[len(s.redoStack)-1]
	s.redoStack = s.redoStack[:len(s.redoStack)-1]
	s.selected = -1
}

func (s *captureSession) undo() {
	s.undoStep()
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *captureSession) redo() {
	s.redoStep()
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *captureSession) deleteSelected() {
	if s.selected < 0 || s.selected >= len(s.marks) {
		return
	}
	s.pushUndo()
	s.marks = append(s.marks[:s.selected], s.marks[s.selected+1:]...)
	s.selected = -1
	if s.win != nil {
		s.win.Invalidate()
	}
}

// ---------------- 工具栏 ----------------

func (s *captureSession) toolbar(c *ui.Context) {
	sel := normRect(s.sel)
	const tbH = 46
	tby := sel.Y + sel.H + 12 // 先放选区下方
	if tby+tbH > s.H {        // 下方放不下，放上方
		tby = sel.Y - tbH - 12
	}
	if tby < 6 { // 上下都放不下，贴屏幕底部（暗区）
		tby = s.H - tbH - 8
	}
	tbx := clamp(sel.X, 8, s.W-8)
	ui.Row(c).Absolute().Left(tbx).Top(tby).
		Padding(6).Gap(4).Radius(10).Background(ui.RGBA(30, 31, 36, 0.96)).
		Border(1, ui.RGBA(255, 255, 255, 0.16)).Children(func() {
		s.toolButton(c, "cursor", "选择", "select")
		s.toolButton(c, "rect", "矩形", "rect")
		s.toolButton(c, "ellipse", "椭圆", "ellipse")
		s.toolButton(c, "line", "直线", "line")
		s.toolButton(c, "arrowline", "箭头", "arrow")
		s.toolButton(c, "pen", "画笔", "pen")
		s.toolButton(c, "text", "文字", "text")
		s.toolButton(c, "mosaic", "马赛克", "mosaic")
		divider(c)
		for _, col := range captureColors {
			s.colorSwatch(c, col)
		}
		divider(c)
		if iconBtn(c, "undo", "撤销").Clicked() {
			s.undo()
		}
		if iconBtn(c, "redo", "重做").Clicked() {
			s.redo()
		}
		if iconBtn(c, "trash", "删除").Clicked() {
			s.deleteSelected()
		}
		divider(c)
		if s.actionBtn(c, "copy", "复制", true).Clicked() {
			s.complete(actCopy)
		}
		if s.actionBtn(c, "save", "保存", false).Clicked() {
			s.complete(actSave)
		}
		if iconBtn(c, "close", "取消").Clicked() {
			s.cancel()
		}
	})
}

var captureColors = []ui.Color{
	ui.RGBA(244, 63, 63, 1),
	ui.RGBA(245, 158, 11, 1),
	ui.RGBA(74, 222, 128, 1),
	ui.RGBA(59, 130, 246, 1),
	ui.RGBA(255, 255, 255, 1),
	ui.RGBA(0, 0, 0, 1),
}

func (s *captureSession) toolButton(c *ui.Context, iconName, label, tool string) {
	active := s.tool == tool
	b := ui.Button(c, "").Label(label).Size(32, 30).Radius(7).Padding(0).Center()
	fg := ui.RGBA(220, 220, 225, 1)
	if active {
		b.Background(colAccentSoft)
		fg = colAccentStr
	}
	b.Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(15).TextColor(fg)
	})
	if b.Clicked() {
		s.tool = tool
	}
}

// actionBtn 是工具栏右侧的主操作按钮（复制/保存），纯图标、居中。
func (s *captureSession) actionBtn(c *ui.Context, iconName, label string, accent bool) ui.Element {
	b := ui.Button(c, "").Label(label).Size(34, 30).Radius(7).Padding(0).Center()
	fg := ui.RGBA(220, 220, 225, 1)
	if accent {
		b.Background(colAccent)
		fg = colKnob
	}
	b.Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(16).TextColor(fg)
	})
	return b
}

func divider(c *ui.Context) {
	ui.Box(c).Width(1).Height(20).Background(ui.RGBA(255, 255, 255, 0.14)).Margin(2, 4)
}

func iconBtn(c *ui.Context, iconName, label string) ui.Element {
	b := ui.Button(c, "").Label(label).Size(30, 30).Radius(7).Padding(0).Center()
	b.Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(15).TextColor(ui.RGBA(220, 220, 225, 1))
	})
	return b
}

func (s *captureSession) colorSwatch(c *ui.Context, col ui.Color) {
	b := ui.Button(c, "").Label("颜色").Size(20, 20).Radius(5).Padding(0).Background(col)
	if col == s.color {
		b.Border(2, ui.RGBA(255, 255, 255, 0.9))
	}
	if b.Clicked() {
		s.color = col
	}
}

// ---------------- 输出 ----------------

func (s *captureSession) complete(action captureAction) {
	sel := normRect(s.sel)
	if !s.hasSel || sel.W < 1 || sel.H < 1 {
		s.cancel()
		return
	}
	data, err := s.win.CapturePage()
	if err != nil {
		s.app.toast("截图失败：" + err.Error())
		return
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		s.app.toast("截图失败：" + err.Error())
		return
	}
	b := img.Bounds()
	sx := float32(b.Dx()) / s.W
	sy := float32(b.Dy()) / s.H
	const inset = 1.0
	x0 := int(math.Round(float64((sel.X + inset) * sx)))
	y0 := int(math.Round(float64((sel.Y + inset) * sy)))
	x1 := int(math.Round(float64((sel.X + sel.W - inset) * sx)))
	y1 := int(math.Round(float64((sel.Y + sel.H - inset) * sy)))
	x0 = clampInt(x0, b.Min.X, b.Max.X)
	y0 = clampInt(y0, b.Min.Y, b.Max.Y)
	x1 = clampInt(x1, x0+1, b.Max.X)
	y1 = clampInt(y1, y0+1, b.Max.Y)

	sub := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	for y := 0; y < y1-y0; y++ {
		for x := 0; x < x1-x0; x++ {
			sub.Set(x, y, img.At(x0+x, y0+y))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, sub); err != nil {
		s.app.toast("截图失败：" + err.Error())
		return
	}

	out := buf.Bytes()
	if action == actCopy {
		if err := mygo.Clipboard.WriteImage(out); err != nil {
			s.app.toast("复制失败：" + err.Error())
			return
		}
		s.app.toast("已复制到剪贴板")
	} else {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       "保存截图",
			DefaultPath: "Screenshot.png",
			Filters:     []mygo.FileFilter{{Name: "PNG 图片", Extensions: []string{"png"}}},
		})
		if err != nil || path == "" {
			// 用户取消保存；保留覆盖层继续编辑
			return
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			s.app.toast("保存失败：" + err.Error())
			return
		}
		s.app.toast("已保存到 " + path)
	}
	s.close()
}

func (s *captureSession) cancel() {
	s.close()
}

func (s *captureSession) close() {
	if s.win != nil {
		s.win.Destroy()
	}
	s.app.capture = nil
	if s.restoreMain && s.app.win != nil {
		s.app.win.Show()
	}
}
