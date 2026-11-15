package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestClipWindowRect(t *testing.T) {
	area := mygo.Rectangle{X: 100, Y: 50, Width: 400, Height: 300}
	cases := []struct {
		name string
		win  mygo.Rectangle
		want ui.Rect
		ok   bool
	}{
		{"完全在屏幕内", mygo.Rectangle{X: 120, Y: 80, Width: 200, Height: 100}, ui.Rect{X: 20, Y: 30, W: 200, H: 100}, true},
		{"左上越界", mygo.Rectangle{X: 50, Y: 0, Width: 200, Height: 100}, ui.Rect{X: 0, Y: 0, W: 150, H: 50}, true},
		{"右下越界", mygo.Rectangle{X: 400, Y: 300, Width: 200, Height: 100}, ui.Rect{X: 300, Y: 250, W: 100, H: 50}, true},
		{"完全在屏幕外", mygo.Rectangle{X: 600, Y: 400, Width: 100, Height: 100}, ui.Rect{}, false},
		{"裁剪后太小", mygo.Rectangle{X: 499, Y: 349, Width: 100, Height: 100}, ui.Rect{}, false},
	}
	for _, c := range cases {
		got, ok := clipWindowRect(area, c.win)
		if ok != c.ok {
			t.Fatalf("%s：ok=%v，期望 %v", c.name, ok, c.ok)
		}
		if ok && got != c.want {
			t.Fatalf("%s：got %+v，期望 %+v", c.name, got, c.want)
		}
	}
}

// 副屏在主屏左侧时 area 与窗口都是负坐标。
func TestLocalWindowRectsNegativeOrigin(t *testing.T) {
	area := mygo.Rectangle{X: -1920, Y: 0, Width: 1920, Height: 1080}
	wins := []mygo.Rectangle{
		{X: -1900, Y: -20, Width: 400, Height: 300},
		{X: 2000, Y: 100, Width: 400, Height: 300}, // 另一块屏，丢弃
	}
	got := localWindowRects(area, wins)
	if len(got) != 1 {
		t.Fatalf("期望 1 个窗口，得到 %d 个：%+v", len(got), got)
	}
	want := ui.Rect{X: 20, Y: 0, W: 400, H: 280}
	if got[0] != want {
		t.Fatalf("got %+v，期望 %+v", got[0], want)
	}
}

// 过小的窗口不参与悬停，z 序保持不变。
func TestLocalWindowRectsFilterAndOrder(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 0, Width: 400, Height: 300}
	wins := []mygo.Rectangle{
		{X: 10, Y: 10, Width: 100, Height: 100}, // 最上层，保留
		{X: 20, Y: 20, Width: 10, Height: 10},   // 太小，丢弃
		{X: 30, Y: 30, Width: 200, Height: 150},
	}
	got := localWindowRects(area, wins)
	if len(got) != 2 {
		t.Fatalf("期望 2 个窗口，得到 %d 个：%+v", len(got), got)
	}
	if got[0] != (ui.Rect{X: 10, Y: 10, W: 100, H: 100}) || got[1] != (ui.Rect{X: 30, Y: 30, W: 200, H: 150}) {
		t.Fatalf("z 序或矩形不对：%+v", got)
	}
}

func TestWindowAt(t *testing.T) {
	wins := []ui.Rect{
		{X: 20, Y: 20, W: 100, H: 100},
		{X: 60, Y: 60, W: 100, H: 100},
	}
	// 重叠区域取最上层（下标 0）。
	if got := windowAt(ui.Point{X: 80, Y: 80}, wins); got != 0 {
		t.Fatalf("重叠区域应命中最上层，得到 %d", got)
	}
	// 边界点算命中（与 pointInRect 的闭区间一致）。
	if got := windowAt(ui.Point{X: 20, Y: 20}, wins); got != 0 {
		t.Fatalf("边界点应命中，得到 %d", got)
	}
	// 第二个窗口独有的区域。
	if got := windowAt(ui.Point{X: 150, Y: 150}, wins); got != 1 {
		t.Fatalf("应命中的第 1 个窗口，得到 %d", got)
	}
	if got := windowAt(ui.Point{X: 300, Y: 200}, wins); got != -1 {
		t.Fatalf("窗口外应返回 -1，得到 %d", got)
	}
	if got := windowAt(ui.Point{X: 10, Y: 10}, nil); got != -1 {
		t.Fatalf("没有候选窗口时应返回 -1，得到 %d", got)
	}
}

func newHoverSession(wins ...ui.Rect) *captureSession {
	s := newTestSession()
	s.windows = wins
	return s
}

// 悬停跟随指针，并在移出所有窗口后清空。
func TestHoverFollowsPointer(t *testing.T) {
	s := newHoverSession(
		ui.Rect{X: 20, Y: 20, W: 100, H: 100},
		ui.Rect{X: 200, Y: 20, W: 100, H: 100},
	)
	tt := ui.NewTester(s.view, 400, 300)

	tt.Move(50, 50)
	if s.hover != 0 {
		t.Fatalf("应悬停第 0 个窗口，得到 %d", s.hover)
	}
	tt.Move(250, 50)
	if s.hover != 1 {
		t.Fatalf("应悬停第 1 个窗口，得到 %d", s.hover)
	}
	tt.Move(150, 250)
	if s.hover != -1 {
		t.Fatalf("窗口外应清除悬停，得到 %d", s.hover)
	}
}

// 在窗口上单击（按下抬起没有位移）直接把窗口矩形作为选区。
func TestClickSelectsWindow(t *testing.T) {
	win := ui.Rect{X: 40, Y: 40, W: 120, H: 80}
	s := newHoverSession(win)
	tt := ui.NewTester(s.view, 400, 300)

	tt.Move(60, 60)
	tt.Press(60, 60)
	tt.Release(60, 60)
	tt.Frame()

	if !s.hasSel || s.sel != win {
		t.Fatalf("没有选中窗口：hasSel=%v sel=%+v", s.hasSel, s.sel)
	}
	if s.hover != -1 {
		t.Fatalf("选中后应清除悬停，得到 %d", s.hover)
	}
	// 选窗口只是建立选区，不应该顺带输出。
	if s.pending != nil {
		t.Fatalf("单击选窗不应触发输出，pending=%v", s.pending)
	}
}

// 按下的轻微抖动仍然算单击选窗。
func TestClickWithSmallDriftSelectsWindow(t *testing.T) {
	win := ui.Rect{X: 40, Y: 40, W: 120, H: 80}
	s := newHoverSession(win)
	tt := ui.NewTester(s.view, 400, 300)

	tt.Press(60, 60)
	tt.Move(62, 61)
	tt.Release(62, 61)
	if !s.hasSel || s.sel != win {
		t.Fatalf("轻微抖动后没有选中窗口：hasSel=%v sel=%+v", s.hasSel, s.sel)
	}
}

// 从窗口内部开始拖动时，仍然按普通框选处理。
func TestDragInsideWindowBecomesFreeSelection(t *testing.T) {
	s := newHoverSession(ui.Rect{X: 40, Y: 40, W: 200, H: 150})
	tt := ui.NewTester(s.view, 400, 300)

	tt.Press(60, 60)
	tt.Move(120, 100)
	tt.Move(180, 150)
	tt.Release(180, 150)

	want := ui.Rect{X: 60, Y: 60, W: 120, H: 90}
	if !s.hasSel || s.sel != want {
		t.Fatalf("应退化成自由框选：hasSel=%v sel=%+v", s.hasSel, s.sel)
	}
}

// 中间没有收到 Move、抬起时已经在阈值外：按框选取尾，不能丢掉这次操作。
func TestReleaseBeyondSlopWithoutMove(t *testing.T) {
	s := newHoverSession(ui.Rect{X: 40, Y: 40, W: 200, H: 150})
	tt := ui.NewTester(s.view, 400, 300)

	tt.Press(60, 60)
	tt.Release(300, 200)

	want := ui.Rect{X: 60, Y: 60, W: 240, H: 140}
	if !s.hasSel || s.sel != want {
		t.Fatalf("应按框选取尾：hasSel=%v sel=%+v", s.hasSel, s.sel)
	}
}

// 点在所有窗口之外：与改造前一样，原地单击不会留下选区。
func TestClickOutsideWindowsLeavesNoSelection(t *testing.T) {
	s := newHoverSession(ui.Rect{X: 40, Y: 40, W: 100, H: 100})
	tt := ui.NewTester(s.view, 400, 300)

	tt.Move(300, 250)
	tt.Press(300, 250)
	tt.Release(300, 250)
	if s.hasSel {
		t.Fatalf("窗口外单击不应留下选区：%+v", s.sel)
	}
}

// 已经有选区后移动鼠标，悬停不再更新。
func TestHoverStopsAfterSelection(t *testing.T) {
	s := newHoverSession(
		ui.Rect{X: 20, Y: 20, W: 100, H: 100},
		ui.Rect{X: 200, Y: 20, W: 100, H: 100},
	)
	tt := ui.NewTester(s.view, 400, 300)

	tt.Press(50, 50)
	tt.Release(50, 50)
	if !s.hasSel {
		t.Fatal("没有选中第一个窗口")
	}

	tt.Move(250, 50)
	if s.hover != -1 {
		t.Fatalf("有选区后不应再更新悬停，得到 %d", s.hover)
	}
	if _, ok := s.hoverRect(); ok {
		t.Fatal("有选区时 hoverRect 必须返回 false，否则悬停边框会被画进输出图")
	}
}

// 窗口上双击：第一次单击选中窗口，第二次走原有的「双击选区复制」。
func TestDoubleClickOnWindowCopies(t *testing.T) {
	s := newHoverSession(ui.Rect{X: 40, Y: 40, W: 120, H: 80})
	tt := ui.NewTester(s.view, 400, 300)

	tt.ClickAt(60, 60)
	tt.ClickAt(60, 60)
	if s.pending == nil || *s.pending != actCopy {
		t.Fatalf("双击窗口没有触发复制，pending=%v", s.pending)
	}
}

// 用纯灰的「冻结画面」渲染，才能看出压暗与还原的区别。
func grayBitmap(t *testing.T, w, h int) *ui.Bitmap {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 128, 128, 128, 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码测试画面失败：%v", err)
	}
	b, err := ui.DecodeBitmap(buf.Bytes())
	if err != nil {
		t.Fatalf("解码测试画面失败：%v", err)
	}
	return b
}

func grayAt(img *image.RGBA, x, y int) int {
	r, _, _, _ := img.At(x, y).RGBA()
	return int(r >> 8)
}

// 悬停时窗口内部恢复清晰、其余压暗，并且窗口边缘有一圈白边。
func TestHoverRestoresAndStrokes(t *testing.T) {
	win := ui.Rect{X: 100, Y: 120, W: 200, H: 100}
	s := newHoverSession(win)
	s.bitmap = grayBitmap(t, 400, 300)
	s.hover = 0

	tt := ui.NewTester(s.view, 400, 300)
	tt.Frame()
	img := tt.Image()
	if img == nil {
		t.Fatal("渲染结果为空")
	}

	if got := grayAt(img, 200, 170); got < 120 {
		t.Fatalf("悬停窗口内部没有恢复清晰，灰度=%d", got)
	}
	if got := grayAt(img, 20, 280); got > 100 {
		t.Fatalf("窗口外应保持压暗，灰度=%d", got)
	}
	if got := grayAt(img, 200, 120); got < 200 {
		t.Fatalf("悬停窗口没有白边，灰度=%d", got)
	}
}

// 已经有选区时不能再画悬停：否则高亮会被 CapturePage 截进输出图。
func TestHoverHiddenWhenSelectionExists(t *testing.T) {
	s := newHoverSession(ui.Rect{X: 100, Y: 120, W: 200, H: 100})
	s.bitmap = grayBitmap(t, 400, 300)
	s.hover = 0
	s.hasSel = true
	s.sel = ui.Rect{X: 20, Y: 20, W: 60, H: 60}

	tt := ui.NewTester(s.view, 400, 300)
	tt.Frame()
	img := tt.Image()
	if img == nil {
		t.Fatal("渲染结果为空")
	}

	if got := grayAt(img, 200, 200); got > 100 {
		t.Fatalf("有选区时窗口区域不该被还原，灰度=%d", got)
	}
	if got := grayAt(img, 200, 120); got > 150 {
		t.Fatalf("有选区时不该画悬停白边，灰度=%d", got)
	}
}
