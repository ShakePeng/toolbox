package main

import (
	"image"
	"math"

	"github.com/egoist/mygo/ui"
)

// markKind 是标注类型。
type markKind string

const (
	mkRect    markKind = "rect"
	mkEllipse markKind = "ellipse"
	mkLine    markKind = "line"
	mkArrow   markKind = "arrow"
	mkPen     markKind = "pen"
	mkText    markKind = "text"
	mkMosaic  markKind = "mosaic"
)

// mark 是一条标注。形状类用 rect（可为负宽高，绘制前归一化）；
// 直线/箭头用 a、b 端点；画笔用 pts；文字用 rect 的位置 + text。
type mark struct {
	kind  markKind
	rect  ui.Rect
	a, b  ui.Point
	pts   []ui.Point
	text  string
	color ui.Color
	width float32
	size  float32
}

// ---------------- 纯几何（可单测） ----------------

// normRect 把可能反方向的矩形归一化成正宽高。
func normRect(r ui.Rect) ui.Rect {
	if r.W < 0 {
		r.X += r.W
		r.W = -r.W
	}
	if r.H < 0 {
		r.Y += r.H
		r.H = -r.H
	}
	return r
}

// rectFromPoints 由两个拖拽点构造归一化矩形。
func rectFromPoints(a, b ui.Point) ui.Rect {
	return normRect(ui.Rect{X: a.X, Y: a.Y, W: b.X - a.X, H: b.Y - a.Y})
}

func pointInRect(p ui.Point, r ui.Rect) bool {
	return p.X >= r.X && p.X <= r.X+r.W && p.Y >= r.Y && p.Y <= r.Y+r.H
}

// clamp 把 v 限制在 [lo, hi]。
func clamp(v, lo, hi float32) float32 {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// distToSeg 是点 p 到线段 ab 的距离，用于命中直线/画笔。
func distToSeg(p, a, b ui.Point) float32 {
	dx, dy := b.X-a.X, b.Y-a.Y
	if dx == 0 && dy == 0 {
		return float32(math.Hypot(float64(p.X-a.X), float64(p.Y-a.Y)))
	}
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / (dx*dx + dy*dy)
	t = clamp(t, 0, 1)
	cx, cy := a.X+t*dx, a.Y+t*dy
	return float32(math.Hypot(float64(p.X-cx), float64(p.Y-cy)))
}

// markHit 判断点 p 是否命中某条标注（带一点容差）。
func markHit(p ui.Point, m mark, tol float32) bool {
	switch m.kind {
	case mkLine, mkArrow:
		return distToSeg(p, m.a, m.b) <= tol
	case mkPen:
		for i := 1; i < len(m.pts); i++ {
			if distToSeg(p, m.pts[i-1], m.pts[i]) <= tol {
				return true
			}
		}
		return false
	default:
		return pointInRect(p, normRect(m.rect))
	}
}

// translateMark 平移一条标注。
func translateMark(m *mark, dx, dy float32) {
	switch m.kind {
	case mkLine, mkArrow:
		m.a.X += dx
		m.a.Y += dy
		m.b.X += dx
		m.b.Y += dy
	case mkPen:
		for i := range m.pts {
			m.pts[i].X += dx
			m.pts[i].Y += dy
		}
	default:
		m.rect.X += dx
		m.rect.Y += dy
	}
}

// cloneMarks 深拷贝，供撤销/重做保存快照。
func cloneMarks(src []mark) []mark {
	out := make([]mark, len(src))
	for i, m := range src {
		out[i] = m
		if m.pts != nil {
			out[i].pts = append([]ui.Point(nil), m.pts...)
		}
	}
	return out
}

// ---------------- 绘制 ----------------

// drawMark 用 painter 画一条标注。src 与 scale 供马赛克采样原始像素。
func drawMark(p *ui.Painter, m mark, src image.Image, scale float32) {
	switch m.kind {
	case mkRect:
		r := normRect(m.rect)
		p.Stroke(r, m.color, 0, m.width)
	case mkEllipse:
		r := normRect(m.rect)
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		p.StrokePath(ellipsePath(cx, cy, r.W/2, r.H/2), m.width, m.color)
	case mkLine:
		p.StrokePath(linePath(m.a, m.b), m.width, m.color)
	case mkArrow:
		p.StrokePath(linePath(m.a, m.b), m.width, m.color)
		p.FillPath(arrowHead(m.a, m.b, m.width), m.color)
	case mkPen:
		if len(m.pts) >= 2 {
			path := &ui.Path{}
			path.MoveTo(m.pts[0].X, m.pts[0].Y)
			for _, pt := range m.pts[1:] {
				path.LineTo(pt.X, pt.Y)
			}
			path.Cap(ui.RoundCap).Join(ui.RoundJoin)
			p.StrokePath(path, m.width, m.color)
		}
	case mkText:
		// 绘制原点就是点击位置，和无外观的输入框对齐，
		// 提交前后文字停在原地，不会突然往下跳一个行高。
		p.Text(m.rect.X, m.rect.Y, m.text, m.size, m.color)
	case mkMosaic:
		drawMosaicBrush(p, m, src, scale)
	}
}

// distToPath 是点 p 到折线 pts 的最短距离（单点则到点距离）。
func distToPath(p ui.Point, pts []ui.Point) float32 {
	if len(pts) == 0 {
		return 1e9
	}
	if len(pts) == 1 {
		return float32(math.Hypot(float64(p.X-pts[0].X), float64(p.Y-pts[0].Y)))
	}
	best := float32(1e9)
	for i := 1; i < len(pts); i++ {
		if d := distToSeg(p, pts[i-1], pts[i]); d < best {
			best = d
		}
	}
	return best
}

// drawMosaicBrush 沿画笔路径做像素化涂抹（马赛克笔刷）。
func drawMosaicBrush(p *ui.Painter, m mark, src image.Image, scale float32) {
	if src == nil || len(m.pts) == 0 {
		return
	}
	// 笔刷比线宽稍粗一点点即可：块决定像素化粒度，radius 决定笔迹宽度。
	block := m.width * 2
	if block < 6 {
		block = 6
	}
	radius := m.width * 1.5
	if radius < block/2 {
		radius = block / 2
	}
	// 路径包围盒
	minX, minY, maxX, maxY := m.pts[0].X, m.pts[0].Y, m.pts[0].X, m.pts[0].Y
	for _, pt := range m.pts {
		minX, minY = minf(minX, pt.X), minf(minY, pt.Y)
		maxX, maxY = maxf(maxX, pt.X), maxf(maxY, pt.Y)
	}
	x0 := float32(math.Floor(float64((minX-radius)/block))) * block
	y0 := float32(math.Floor(float64((minY-radius)/block))) * block
	for y := y0; y <= maxY+radius; y += block {
		for x := x0; x <= maxX+radius; x += block {
			cx, cy := x+block/2, y+block/2
			if distToPath(ui.Point{X: cx, Y: cy}, m.pts) > radius+block/2 {
				continue
			}
			col := averageColor(src, x*scale, y*scale, (x+block)*scale, (y+block)*scale)
			p.Fill(ui.Rect{X: x, Y: y, W: block, H: block}, col, 0)
		}
	}
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// ellipsePath 用四段三次贝塞尔近似椭圆。
func ellipsePath(cx, cy, rx, ry float32) *ui.Path {
	const k = 0.5522847498307936
	kx, ky := rx*k, ry*k
	p := &ui.Path{}
	p.MoveTo(cx+rx, cy)
	p.CubeTo(cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry)
	p.CubeTo(cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy)
	p.CubeTo(cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry)
	p.CubeTo(cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy)
	p.Close()
	return p
}

// linePath 返回 a→b 的线段路径。
// 注意不能用 painter.Line：它内部是给两点包围盒填一个轴对齐矩形，
// 斜线会被压成横条或竖条，所以这里统一用描边路径。
func linePath(a, b ui.Point) *ui.Path {
	p := &ui.Path{}
	p.MoveTo(a.X, a.Y)
	p.LineTo(b.X, b.Y)
	p.Cap(ui.RoundCap)
	return p
}

// arrowHead 返回箭头尖端的实心三角形。
func arrowHead(a, b ui.Point, width float32) *ui.Path {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := float32(math.Hypot(float64(dx), float64(dy)))
	if l == 0 {
		l = 1
	}
	ux, uy := dx/l, dy/l
	px, py := -uy, ux
	head := width*3 + 8
	base := ui.Point{X: b.X - ux*head, Y: b.Y - uy*head}
	half := head * 0.5
	path := &ui.Path{}
	path.MoveTo(b.X, b.Y)
	path.LineTo(base.X+px*half, base.Y+py*half)
	path.LineTo(base.X-px*half, base.Y-py*half)
	path.Close()
	return path
}

// drawMosaic 用块平均色覆盖矩形，形成像素化马赛克。
func drawMosaic(p *ui.Painter, r ui.Rect, m mark, src image.Image, scale float32) {
	if src == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	block := m.width * 4
	if block < 8 {
		block = 8
	}
	for y := r.Y; y < r.Y+r.H; y += block {
		for x := r.X; x < r.X+r.W; x += block {
			w := minf(block, r.X+r.W-x)
			h := minf(block, r.Y+r.H-y)
			col := averageColor(src, x*scale, y*scale, (x+w)*scale, (y+h)*scale)
			p.Fill(ui.Rect{X: x, Y: y, W: w, H: h}, col, 0)
		}
	}
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// averageColor 对 src 的一块像素取平均色（device 像素坐标）。
func averageColor(src image.Image, x0, y0, x1, y1 float32) ui.Color {
	b := src.Bounds()
	ix0 := clampInt(int(x0), b.Min.X, b.Max.X)
	iy0 := clampInt(int(y0), b.Min.Y, b.Max.Y)
	ix1 := clampInt(int(x1), ix0+1, b.Max.X)
	iy1 := clampInt(int(y1), iy0+1, b.Max.Y)
	var rs, gs, bs, n uint64
	// 采样步长，避免大块逐像素过慢。
	stepX := maxInt(1, (ix1-ix0)/8)
	stepY := maxInt(1, (iy1-iy0)/8)
	for y := iy0; y < iy1; y += stepY {
		for x := ix0; x < ix1; x += stepX {
			r, g, bl, _ := src.At(x, y).RGBA()
			rs += uint64(r >> 8)
			gs += uint64(g >> 8)
			bs += uint64(bl >> 8)
			n++
		}
	}
	if n == 0 {
		return ui.Color{R: 128, G: 128, B: 128, A: 255}
	}
	return ui.Color{R: uint8(rs / n), G: uint8(gs / n), B: uint8(bs / n), A: 255}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
