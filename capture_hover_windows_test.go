//go:build windows

package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// 物理像素按显示器自己的 Bounds 线性映射，100% / 150% 缩放都要对得上。
func TestPixelRectToDIP(t *testing.T) {
	cases := []struct {
		name  string
		win   gdiRect
		monPx gdiRect
		disp  mygo.Display
		want  mygo.Rectangle
	}{
		{
			name:  "100% 缩放",
			win:   gdiRect{Left: 0, Top: 0, Right: 1280, Bottom: 720},
			monPx: gdiRect{Left: 0, Top: 0, Right: 2560, Bottom: 1440},
			disp:  mygo.Display{Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 2560, Height: 1440}},
			want:  mygo.Rectangle{X: 0, Y: 0, Width: 1280, Height: 720},
		},
		{
			name:  "150% 缩放",
			win:   gdiRect{Left: 0, Top: 0, Right: 960, Bottom: 540},
			monPx: gdiRect{Left: 0, Top: 0, Right: 1920, Bottom: 1080},
			disp:  mygo.Display{Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1280, Height: 720}},
			want:  mygo.Rectangle{X: 0, Y: 0, Width: 640, Height: 360},
		},
		{
			name:  "副屏负坐标",
			win:   gdiRect{Left: -1920, Top: 0, Right: -960, Bottom: 540},
			monPx: gdiRect{Left: -1920, Top: 0, Right: 0, Bottom: 1080},
			disp:  mygo.Display{Bounds: mygo.Rectangle{X: -1280, Y: 0, Width: 1280, Height: 720}},
			want:  mygo.Rectangle{X: -1280, Y: 0, Width: 640, Height: 360},
		},
		{
			name:  "显示器矩形无效",
			win:   gdiRect{Left: 0, Top: 0, Right: 10, Bottom: 10},
			monPx: gdiRect{Left: 100, Top: 100, Right: 100, Bottom: 100},
			disp:  mygo.Display{Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 100, Height: 100}},
			want:  mygo.Rectangle{},
		},
	}
	for _, c := range cases {
		if got := pixelRectToDIP(c.win, c.monPx, c.disp); got != c.want {
			t.Fatalf("%s：got %+v，期望 %+v", c.name, got, c.want)
		}
	}
}

func TestAcceptWindow(t *testing.T) {
	const self = uint32(4242)
	base := winCand{
		pid:      100,
		visible:  true,
		titleLen: 6,
		rect:     gdiRect{Left: 0, Top: 0, Right: 800, Bottom: 600},
	}
	cases := []struct {
		name string
		c    winCand
		want bool
	}{
		{"普通应用窗口", base, true},
		{"不可见", func() winCand { c := base; c.visible = false; return c }(), false},
		{"最小化", func() winCand { c := base; c.iconic = true; return c }(), false},
		{"其他虚拟桌面（cloaked）", func() winCand { c := base; c.cloaked = true; return c }(), false},
		{"本进程窗口", func() winCand { c := base; c.pid = self; return c }(), false},
		{"拿不到 PID", func() winCand { c := base; c.pid = 0; return c }(), false},
		{"工具窗口", func() winCand { c := base; c.exStyle = wsExToolWindow; return c }(), false},
		{"无标题", func() winCand { c := base; c.titleLen = 0; return c }(), false},
		{"无标题但是应用窗口", func() winCand { c := base; c.titleLen = 0; c.exStyle = wsExAppWindow; return c }(), true},
		{"桌面 Progman", func() winCand { c := base; c.class = "Progman"; return c }(), false},
		{"任务栏", func() winCand { c := base; c.class = "Shell_TrayWnd"; return c }(), false},
		{"空矩形", func() winCand { c := base; c.rect = gdiRect{Left: 10, Top: 10, Right: 10, Bottom: 10}; return c }(), false},
	}
	for _, c := range cases {
		if got := acceptWindow(c.c, self); got != c.want {
			t.Fatalf("%s：got %v，期望 %v", c.name, got, c.want)
		}
	}
}
