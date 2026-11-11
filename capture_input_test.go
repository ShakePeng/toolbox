package main

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func newTestSession() *captureSession {
	return &captureSession{
		app: &app{},
		W:   400, H: 300, scale: 1,
		tool: "select", color: ui.RGBA(244, 63, 63, 1), strokeWidth: 3,
		selected: -1,
	}
}

// 模拟一次拖拽框选。
func dragSel(tt *ui.Tester, x0, y0, x1, y1 float32) {
	tt.Press(x0, y0)
	tt.Move((x0+x1)/2, (y0+y1)/2)
	tt.Move(x1, y1)
	tt.Release(x1, y1)
}

// 模拟一次拖拽绘制。
func dragDraw(tt *ui.Tester, x0, y0, x1, y1 float32) {
	tt.Press(x0, y0)
	tt.Move((x0+x1)/2, (y0+y1)/2)
	tt.Move(x1, y1)
	tt.Release(x1, y1)
}

func TestCaptureSelectAndLine(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)

	dragSel(tt, 50, 50, 150, 120)
	if !s.hasSel {
		t.Fatalf("框选后 hasSel 应为 true，sel=%+v", s.sel)
	}
	if s.sel.W < 90 || s.sel.W > 110 || s.sel.H < 60 || s.sel.H > 80 {
		t.Fatalf("框选尺寸异常：%+v", s.sel)
	}

	// 画直线
	s.tool = "line"
	dragDraw(tt, 60, 60, 140, 100)
	if len(s.marks) != 1 || s.marks[0].kind != mkLine {
		t.Fatalf("直线未生成：%+v", s.marks)
	}
	m := s.marks[0]
	if m.a.X < 55 || m.a.X > 65 || m.b.X < 135 || m.b.X > 145 {
		t.Fatalf("直线端点异常：a=%+v b=%+v", m.a, m.b)
	}

	// 画箭头
	s.tool = "arrow"
	dragDraw(tt, 70, 70, 130, 110)
	if len(s.marks) != 2 || s.marks[1].kind != mkArrow {
		t.Fatalf("箭头未生成：%+v", s.marks)
	}
}

// 直线必须点对点：斜线要真的斜着画出来，不能被压成横条/竖条。
// 这条守护 mygo 的 painter.Line（它填的是两点包围盒，只会出横/竖条）。
func TestCaptureDiagonalLineRender(t *testing.T) {
	s := newTestSession()
	s.W, s.H = 200, 200
	s.color = ui.RGBA(255, 0, 0, 1)
	tt := ui.NewTester(s.view, 200, 200)

	// 先框选一块区域（选区边界避开断言点，防止命中手柄）
	dragSel(tt, 40, 40, 160, 160)
	if !s.hasSel {
		t.Fatalf("框选失败：%+v", s.sel)
	}

	s.tool = "line"
	dragDraw(tt, 60, 70, 160, 130)
	if len(s.marks) != 1 {
		t.Fatalf("直线未生成：%+v", s.marks)
	}

	tt.Frame()
	img := tt.Image()
	if img == nil {
		t.Fatal("渲染结果为空")
	}
	isRed := func(x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return r>>8 > 150 && g>>8 < 120 && b>>8 < 120
	}
	// 斜线中点应在线上
	if !isRed(110, 100) {
		t.Fatalf("斜线中点 (110,100) 没有颜色，说明没画出斜线")
	}
	// 旧实现（横条）落在这里，必须为空
	if isRed(110, 70) {
		t.Fatalf("(110,70) 被填色，直线退化成了横条")
	}
}

// 使用绘制工具的过程中，工具栏不能被收起。
func TestToolbarStaysVisibleWhileDrawing(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 40, 40, 260, 200)

	s.tool = "rect"
	tt.Press(80, 80)
	tt.Move(160, 140) // 按住不放，处于绘制中
	tt.Frame()
	if _, ok := tt.Find("马赛克"); !ok {
		t.Fatal("绘制中工具栏消失了")
	}
	tt.Release(160, 140)

	// 绘制结束后依然在
	tt.Frame()
	if _, ok := tt.Find("马赛克"); !ok {
		t.Fatal("绘制结束后工具栏消失了")
	}
}

// 绘制工具在选区外拖拽：无反应，不绘制也不重新框选。
func TestDrawToolOutsideSelection(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	s.tool = "rect"
	dragDraw(tt, 60, 60, 140, 120)
	if len(s.marks) != 0 {
		t.Fatalf("选区外不应绘制：%+v", s.marks)
	}
	if s.hasSel {
		t.Fatal("选区外拖拽不应开始框选")
	}
}

// 还没框选时的提示文字要在背景框里居中，不能偏下、偏左。
func TestHintTextCentered(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	tt.Frame()
	img := tt.Image()
	if img == nil {
		t.Fatal("渲染结果为空")
	}

	x0, y0, x1, y1 := 1<<30, 1<<30, -1, -1
	for y := 0; y < 120; y++ {
		for x := 0; x < 400; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 200 && g>>8 > 200 && b>>8 > 200 {
				if x < x0 {
					x0 = x
				}
				if y < y0 {
					y0 = y
				}
				if x > x1 {
					x1 = x
				}
				if y > y1 {
					y1 = y
				}
			}
		}
	}
	if x1 < 0 {
		t.Fatal("没有找到提示文字")
	}
	cx, cy := float64(x0+x1)/2, float64(y0+y1)/2
	if math.Abs(cx-200) > 3 {
		t.Fatalf("提示文字没有水平居中：cx=%.1f", cx)
	}
	// 背景框从 y=44 开始，高度约 31，中心在 59 左右。
	if cy < 52 || cy > 68 {
		t.Fatalf("提示文字没有垂直居中：cy=%.1f", cy)
	}
}

// 选区尺寸文字要落在黑色背景框里，不能跑到框下面。
func TestSelSizeLabelInsideBox(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	// 选区贴着顶部，标签会放进选区内部：框在 (40,14) 起、84×18
	dragSel(tt, 40, 10, 300, 200)
	tt.Frame()
	img := tt.Image()
	if img == nil {
		t.Fatal("渲染结果为空")
	}

	x0, y0, x1, y1 := 1<<30, 1<<30, -1, -1
	for y := 14; y < 32; y++ {
		for x := 40; x < 124; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 200 && g>>8 > 200 && b>>8 > 200 {
				if x < x0 {
					x0 = x
				}
				if y < y0 {
					y0 = y
				}
				if x > x1 {
					x1 = x
				}
				if y > y1 {
					y1 = y
				}
			}
		}
	}
	if x1 < 0 {
		t.Fatal("没有找到尺寸文字")
	}
	// 文字必须完整落在框内（y 14..31）
	if y0 < 14 || y1 > 31 {
		t.Fatalf("尺寸文字超出背景框：文字 y=%d..%d，框 y=14..31", y0, y1)
	}
}

// 左键双击选区：直接复制（不经过工具栏，也不要求当前是选择工具）。
func TestDoubleClickCopiesSelection(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 40, 40, 260, 200)
	if !s.hasSel {
		t.Fatalf("框选失败：%+v", s.sel)
	}

	tt.ClickAt(150, 120)
	tt.ClickAt(150, 120)
	if s.pending == nil || *s.pending != actCopy {
		t.Fatalf("双击没有触发复制，pending=%v", s.pending)
	}
}

// ESC 退出截图（文字编辑中也不例外）。
func TestEscapeExitsCapture(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	tt.Frame()

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !s.closed {
		t.Fatal("按 ESC 没有退出截图")
	}
}

// 文字编辑中按 ESC 也退出截图。
func TestEscapeWhileEditingExitsCapture(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 20, 20, 300, 220)

	s.tool = "text"
	tt.ClickAt(60, 60)
	tt.Type("你好")
	if !s.editingText {
		t.Fatal("没有进入文字输入")
	}

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !s.closed {
		t.Fatal("文字编辑中按 ESC 没有退出截图")
	}
}

// 输入处理出现意外 panic 时必须释放置顶蒙层，不能把它留在异常状态。
func TestInputPanicClosesCapture(t *testing.T) {
	s := newTestSession()
	s.app.capture = s
	s.marks = []mark{{kind: mkRect}}
	s.selected = 0
	s.drag = dsMoveMark
	// 故意留空移动前快照，模拟未预料的索引异常。
	s.pre = nil

	s.onInput(ui.InputEvent{Kind: ui.InputPointerMove, X: 20, Y: 20})
	if !s.closed {
		t.Fatal("输入处理 panic 后蒙层没有关闭")
	}
	if s.app.capture != nil {
		t.Fatal("输入处理 panic 后截图会话没有清理")
	}
}

// 背景上右键退出截图。
func TestRightClickExitsCapture(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	tt.Frame()

	tt.RightClickAt(200, 150)
	if !s.closed {
		t.Fatal("右键没有退出截图")
	}
}

// 工具栏压在背景层之上，右键也必须能退出截图（靠最外层拦截）。
func TestRightClickOnToolbarExitsCapture(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 40, 40, 260, 200)

	r, ok := tt.Find("马赛克")
	if !ok {
		t.Fatal("框选后工具栏没有出现")
	}
	tt.RightClickAt(r.X+r.W/2, r.Y+r.H/2)
	if !s.closed {
		t.Fatal("在工具栏上右键没有退出截图")
	}
}

// 最外层拦截右键，但不能影响工具栏的普通点击。
func TestToolbarClickStillWorks(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 40, 40, 260, 200)

	if err := tt.Click("矩形"); err != nil {
		t.Fatal(err)
	}
	if s.tool != "rect" {
		t.Fatalf("点击工具栏没有切换工具：%s", s.tool)
	}
}

func TestToolbarStrokeWidth(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 40, 40, 260, 200)

	if err := tt.Click("矩形"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("粗线"); err != nil {
		t.Fatal(err)
	}
	if s.strokeWidth != 5 {
		t.Fatalf("点击粗线后线宽应为 5，实际 %v", s.strokeWidth)
	}

	dragDraw(tt, 60, 60, 120, 100)
	if len(s.marks) != 1 || s.marks[0].width != 5 {
		t.Fatalf("新标注没有使用所选线宽：%+v", s.marks)
	}
}

// 文字输入完点别处就落笔，不需要按回车。
func TestTextCommitsOnClickAway(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 20, 20, 300, 220)

	s.tool = "text"
	tt.ClickAt(60, 60)
	if !s.editingText {
		t.Fatal("点选区内没有进入文字输入")
	}
	tt.Type("你好")
	if s.textValue != "你好" {
		t.Fatalf("输入没有进入编辑框：%q", s.textValue)
	}

	// 点到选区外（避开工具栏）→ 自动落笔
	tt.ClickAt(350, 10)
	if s.editingText {
		t.Fatal("点击别处后仍处于编辑状态")
	}
	if len(s.marks) != 1 || s.marks[0].kind != mkText || s.marks[0].text != "你好" {
		t.Fatalf("文字没有自动保存：%+v", s.marks)
	}
}

// 文字落笔后要停在输入时的位置：比对输入框里的字和提交后的字的像素位置。
func TestTextMarkStaysWhereTyped(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 20, 20, 300, 220)

	s.tool = "text"
	tt.ClickAt(60, 60)
	tt.Type("MMMM")
	tt.Frame()

	// 只看点击位置周围，避开工具栏里的红色色块
	topLeft := func() (int, int) {
		img := tt.Image()
		x0, y0 := 1<<30, 1<<30
		for y := 30; y < 150; y++ {
			for x := 30; x < 300; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r>>8 > 150 && g>>8 < 110 && b>>8 < 110 {
					if x < x0 {
						x0 = x
					}
					if y < y0 {
						y0 = y
					}
				}
			}
		}
		return x0, y0
	}

	ex, ey := topLeft()
	if ex == 1<<30 {
		t.Fatal("输入框里的文字没有渲染出来")
	}
	s.commitText()
	tt.Frame()
	cx, cy := topLeft()
	if cx == 1<<30 {
		t.Fatal("提交后的文字没有渲染出来")
	}
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	if abs(cx-ex) > 2 || abs(cy-ey) > 2 {
		t.Fatalf("文字落笔后位置变了：输入 (%d,%d) → 提交 (%d,%d)", ex, ey, cx, cy)
	}
}

// 马赛克是笔刷：在选区内拖拽生成带路径的标记。
func TestMosaicBrush(t *testing.T) {
	s := newTestSession()
	tt := ui.NewTester(s.view, 400, 300)
	dragSel(tt, 20, 20, 210, 210)
	s.tool = "mosaic"
	dragDraw(tt, 60, 60, 160, 120)
	if len(s.marks) != 1 || s.marks[0].kind != mkMosaic {
		t.Fatalf("马赛克未生成：%+v", s.marks)
	}
	if len(s.marks[0].pts) < 2 {
		t.Fatalf("马赛克笔刷应有多个路径点，实际 %d", len(s.marks[0].pts))
	}
}
