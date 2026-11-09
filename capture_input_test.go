package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func newTestSession() *captureSession {
	return &captureSession{
		W: 400, H: 300, scale: 1,
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
