package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 功能中心两列卡片：末行只剩一张时也必须与其他卡片等宽。
// flex 布局会把主轴基准下限钳到 padding+border，卡片自带 34px 基准而
// 裸占位 Box 是 0，末行卡片会多拿 17px（(34-0)/2）。占位块必须复刻卡片
// 的 padding+border 才能等宽。用像素扫描卡片背景色段测量——Find 只能
// 拿文字矩形测不到卡片边框。
func TestHomeFeatureCardsEqualWidth(t *testing.T) {
	a := newTestApp()
	a.settings.HasCompletedOnboarding = true
	tt := ui.NewTester(a.view, 1080, 1500)
	tt.Frame()
	img := tt.Image()
	if img == nil {
		t.Skip("无渲染图像")
	}

	// 卡片背景 colBg = #202124。
	isCard := func(x, y int) bool {
		c := img.RGBAAt(x, y)
		return c.R == 32 && c.G == 33 && c.B == 36 && c.A == 255
	}
	// 扫一行里的连续卡片背景段；短段是图标/徽标背景，按 120px 过滤。
	segs := func(y int) [][2]int {
		var out [][2]int
		start := -1
		for x := 0; x < 1080; x++ {
			if isCard(x, y) {
				if start < 0 {
					start = x
				}
			} else if start >= 0 {
				if x-start > 120 {
					out = append(out, [2]int{start, x})
				}
				start = -1
			}
		}
		if start >= 0 && 1080-start > 120 {
			out = append(out, [2]int{start, 1080})
		}
		return out
	}
	// 标题行往上 8px 落在卡片顶部 padding 内，是纯背景。
	cardRow := func(title string) [][2]int {
		r, ok := tt.Find(title)
		if !ok {
			t.Fatalf("找不到 %q", title)
		}
		return segs(int(r.Y) - 8)
	}

	// VS Code 行是完整双卡行，Fork 是末行单卡，两者卡片必须等宽且左对齐。
	normal := cardRow("VS Code 快速打开")
	last := cardRow("Fork 快速打开")
	if len(normal) < 2 {
		t.Fatalf("VS Code 行应有两张卡片，实际 %v", normal)
	}
	if len(last) != 1 {
		t.Fatalf("Fork 行应只有一张卡片，实际 %v", last)
	}
	want := normal[0][1] - normal[0][0]
	got := last[0][1] - last[0][0]
	if got != want {
		t.Errorf("末行卡片宽 %dpx，正常行卡片宽 %dpx，差 %dpx（应等宽）",
			got, want, got-want)
	}
	if last[0][0] != normal[0][0] {
		t.Errorf("末行卡片起点 x=%d 与正常行 x=%d 不对齐", last[0][0], normal[0][0])
	}
}

// 划词翻译设置弹框里的服务商网格一行三张卡片：默认三个 provider
// 应挤满一行，卡片等宽（浅色主题，卡片 #f7f8fa）。
func TestModalProviderCardsEqualWidth(t *testing.T) {
	a := newTestApp()
	a.settings.HasCompletedOnboarding = true
	tt := ui.NewTester(a.view, 1080, 900)
	if err := tt.Click("划词翻译设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	img := tt.Image()
	if img == nil {
		t.Skip("无渲染图像")
	}

	isCard := func(x, y int) bool {
		c := img.RGBAAt(x, y)
		return c.R == 247 && c.G == 248 && c.B == 250 && c.A == 255
	}
	segs := func(y int) [][2]int {
		var out [][2]int
		start := -1
		for x := 0; x < 1080; x++ {
			if isCard(x, y) {
				if start < 0 {
					start = x
				}
			} else if start >= 0 {
				if x-start > 100 {
					out = append(out, [2]int{start, x})
				}
				start = -1
			}
		}
		if start >= 0 && 1080-start > 100 {
			out = append(out, [2]int{start, 1080})
		}
		return out
	}
	cardRow := func(title string) [][2]int {
		r, ok := tt.Find(title)
		if !ok {
			t.Fatalf("找不到 %q", title)
		}
		// providerCard 顶部是 icon 行，标题行往上找到的第一个卡片背景段即可。
		for dy := -4; dy > -20; dy -= 2 {
			if s := segs(int(r.Y) + dy); len(s) > 0 {
				return s
			}
		}
		return nil
	}

	// 三个 provider（百度/Google/AI）应在一行内三等分。
	row := cardRow("百度翻译")
	if len(row) != 3 {
		t.Fatalf("服务商应一行三张卡片，实际 %v", row)
	}
	widths := make([]int, len(row))
	for i, s := range row {
		widths[i] = s[1] - s[0]
		// 像素扫描受边缘抗锯齿影响，允许 1px 误差。
		if widths[i] != widths[0] && abs(widths[i]-widths[0]) > 1 {
			t.Errorf("三张卡片应等宽：第 %d 张宽 %dpx，首张 %dpx", i, widths[i], widths[0])
		}
	}
	// 卡片间应有 gap（10~20px），不能粘连或重叠。
	for i := 1; i < len(row); i++ {
		gap := row[i][0] - row[i-1][1]
		if gap < 8 || gap > 24 {
			t.Errorf("卡片 %d 与前一张间距 %dpx 异常（应约 14px）", i, gap)
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
