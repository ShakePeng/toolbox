package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNormRect(t *testing.T) {
	r := normRect(ui.Rect{X: 100, Y: 100, W: -40, H: -30})
	if r != (ui.Rect{X: 60, Y: 70, W: 40, H: 30}) {
		t.Fatalf("normRect = %+v", r)
	}
	if rectFromPoints(ui.Point{X: 10, Y: 20}, ui.Point{X: 30, Y: 60}) != (ui.Rect{X: 10, Y: 20, W: 20, H: 40}) {
		t.Fatal("rectFromPoints mismatch")
	}
}

func TestDistToSeg(t *testing.T) {
	a := ui.Point{X: 0, Y: 0}
	b := ui.Point{X: 100, Y: 0}
	if d := distToSeg(ui.Point{X: 50, Y: 10}, a, b); d < 9.9 || d > 10.1 {
		t.Fatalf("dist = %v", d)
	}
	// 超出端点时取到端点距离
	if d := distToSeg(ui.Point{X: -10, Y: 0}, a, b); d < 9.9 || d > 10.1 {
		t.Fatalf("dist to endpoint = %v", d)
	}
}

func TestMarkHitAndTranslate(t *testing.T) {
	rect := mark{kind: mkRect, rect: ui.Rect{X: 10, Y: 10, W: 40, H: 30}}
	if !markHit(ui.Point{X: 20, Y: 20}, rect, 0) {
		t.Fatal("point inside rect should hit")
	}
	if markHit(ui.Point{X: 5, Y: 5}, rect, 0) {
		t.Fatal("point outside rect should miss")
	}
	translateMark(&rect, 5, -5)
	if rect.rect.X != 15 || rect.rect.Y != 5 {
		t.Fatalf("rect translate = %+v", rect.rect)
	}

	line := mark{kind: mkLine, a: ui.Point{X: 0, Y: 0}, b: ui.Point{X: 100, Y: 0}}
	if !markHit(ui.Point{X: 50, Y: 2}, line, 3) {
		t.Fatal("near line should hit")
	}
	translateMark(&line, 10, 10)
	if line.a.X != 10 || line.b.Y != 10 {
		t.Fatal("line translate mismatch")
	}
}

func TestCloneMarks(t *testing.T) {
	src := []mark{{kind: mkPen, pts: []ui.Point{{X: 1, Y: 1}}}}
	dst := cloneMarks(src)
	dst[0].pts[0].X = 99
	if src[0].pts[0].X != 1 {
		t.Fatal("cloneMarks shares pts slice")
	}
}

func TestHandleHit(t *testing.T) {
	s := &captureSession{W: 200, H: 200, hasSel: true, sel: ui.Rect{X: 50, Y: 50, W: 100, H: 100}}
	// 左上角手柄在选区外侧
	c := s.handleCenter(normRect(s.sel), 0)
	if c.X >= 50 || c.Y >= 50 {
		t.Fatalf("top-left handle should sit outside selection: %+v", c)
	}
	if got := s.hitHandle(c); got != 0 {
		t.Fatalf("hitHandle = %d, want 0", got)
	}
	if got := s.hitHandle(ui.Point{X: 100, Y: 100}); got != -1 {
		t.Fatalf("center should not hit a handle: %d", got)
	}
}

func TestClampSel(t *testing.T) {
	s := &captureSession{W: 100, H: 100, hasSel: true, sel: ui.Rect{X: -20, Y: 80, W: 200, H: 200}}
	s.clampSel()
	if s.sel.X != 0 || s.sel.Y != 80 {
		t.Fatalf("clamp origin = %+v", s.sel)
	}
	if s.sel.X+s.sel.W > 100 || s.sel.Y+s.sel.H > 100 {
		t.Fatalf("clamp overflow = %+v", s.sel)
	}
}
