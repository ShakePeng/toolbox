package main

import (
	"image"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestPinBounds(t *testing.T) {
	cases := []struct {
		name string
		area mygo.Rectangle
		sel  ui.Rect
		x, y int
		w, h int
	}{
		{
			name: "普通屏幕",
			area: mygo.Rectangle{X: 100, Y: 50, Width: 1440, Height: 900},
			sel:  ui.Rect{X: 10, Y: 20, W: 120, H: 80},
			x:    111, y: 71, w: 118, h: 78,
		},
		{
			name: "副屏负坐标",
			area: mygo.Rectangle{X: -1920, Y: 0, Width: 1920, Height: 1080},
			sel:  ui.Rect{X: 300, Y: 200, W: 400, H: 300},
			x:    -1619, y: 201, w: 398, h: 298,
		},
		{
			name: "极小选区也不会得到 0 尺寸",
			area: mygo.Rectangle{X: 0, Y: 0, Width: 100, Height: 100},
			sel:  ui.Rect{X: 5, Y: 5, W: 3, H: 3},
			x:    6, y: 6, w: 1, h: 1,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			x, y, w, h := pinBounds(tt.area, tt.sel)
			if x != tt.x || y != tt.y || w != tt.w || h != tt.h {
				t.Fatalf("pinBounds = (%d,%d,%d,%d)，期望 (%d,%d,%d,%d)", x, y, w, h, tt.x, tt.y, tt.w, tt.h)
			}
		})
	}
}

// 贴图窗口能把位图画满整个窗口，不依赖外部窗口即可渲染。
func TestPinRender(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	p := &pinWindow{bmp: ui.NewBitmap(img)}
	tt := ui.NewTester(p.view, 40, 30)
	tt.Frame()
	if tt.Image() == nil {
		t.Fatal("贴图应渲染出画面")
	}
}
