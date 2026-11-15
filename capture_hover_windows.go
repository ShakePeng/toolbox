//go:build windows

package main

import (
	"log"
	"math"
	"os"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
)

// 截图悬停选窗的 Windows 实现：EnumWindows 枚举顶层窗口，再用 DWM 的扩展边框
// 拿「实际画在屏幕上的区域」。
//
// user32/gdi 的 DLL 与 gdiRect/gdiMonitorInfo 复用 capture_windows.go，
// dwmapi 句柄复用 launcher_border_windows.go，避免同包重复加载。
const (
	gwlExStyle = ^uintptr(19) // -20：GetWindowLongPtrW 的 GWL_EXSTYLE

	wsExToolWindow = 0x00000080
	wsExAppWindow  = 0x00040000

	dwmwaExtendedFrameBounds = 9
	dwmwaCloaked             = 14
)

var (
	procEnumWindows              = user32DLL.NewProc("EnumWindows")
	procIsWindowVisible          = user32DLL.NewProc("IsWindowVisible")
	procIsIconic                 = user32DLL.NewProc("IsIconic")
	procGetWindowTextLengthW     = user32DLL.NewProc("GetWindowTextLengthW")
	procGetClassNameW            = user32DLL.NewProc("GetClassNameW")
	procGetWindowThreadProcessID = user32DLL.NewProc("GetWindowThreadProcessId")
	procGetWindowLongPtrW        = user32DLL.NewProc("GetWindowLongPtrW")
	procGetWindowRect            = user32DLL.NewProc("GetWindowRect")
	procDwmGetWindowAttribute    = dwmapiDLL.NewProc("DwmGetWindowAttribute")
)

// winCand 是一个顶层窗口的原始属性；rect 是物理像素、虚拟桌面坐标。
type winCand struct {
	pid      uint32
	rect     gdiRect
	exStyle  uintptr
	titleLen int
	class    string
	visible  bool
	iconic   bool
	cloaked  bool
}

// pickDeniedClasses 是明确不该参与悬停的系统 shell 窗口。
// 任务栏、输入法这类窗口大多还会被「无标题」规则挡掉，这里列出以免误选。
var pickDeniedClasses = map[string]struct{}{
	"Progman":                    {}, // 桌面
	"WorkerW":                    {},
	"Shell_TrayWnd":              {}, // 任务栏
	"Shell_SecondaryTrayWnd":     {},
	"Windows.UI.Core.CoreWindow": {}, // shell 的核心窗
	"ForegroundStaging":          {},
	"MultitaskingViewFrame":      {},
	"TaskListThumbnailWnd":       {},
}

// acceptWindow 决定一个顶层窗口是否参与悬停命中。
func acceptWindow(c winCand, self uint32) bool {
	if !c.visible || c.iconic || c.cloaked {
		return false
	}
	// 本进程的窗口（主窗口、贴图、托盘等）不参与。
	if c.pid == 0 || c.pid == self {
		return false
	}
	if c.exStyle&wsExToolWindow != 0 {
		return false
	}
	// 任务栏、输入法、悬浮提示这类顶层窗口通常没有标题；用 WS_EX_APPWINDOW
	// 放行「没有标题栏但确实是应用」的窗口。
	if c.titleLen == 0 && c.exStyle&wsExAppWindow == 0 {
		return false
	}
	if _, denied := pickDeniedClasses[c.class]; denied {
		return false
	}
	return c.rect.Right > c.rect.Left && c.rect.Bottom > c.rect.Top
}

// listCaptureWindowsPlatform 返回当前所有可见应用窗口的屏幕 DIP 矩形。
// 按 EnumWindows 的枚举顺序返回（实际为 z 序自上而下）。
func listCaptureWindowsPlatform(disp mygo.Display) []mygo.Rectangle {
	if disp.ID == 0 || disp.Bounds.Width <= 0 || disp.Bounds.Height <= 0 {
		return nil
	}
	var mon gdiMonitorInfo
	mon.Size = uint32(unsafe.Sizeof(mon))
	if ok, _, _ := procGetMonitorInfoW.Call(uintptr(disp.ID), uintptr(unsafe.Pointer(&mon))); ok == 0 {
		return nil
	}

	self := uint32(os.Getpid())
	var out []mygo.Rectangle
	enumWindows(func(hwnd uintptr) {
		c := windowCandidate(hwnd)
		if !acceptWindow(c, self) {
			return
		}
		out = append(out, pixelRectToDIP(c.rect, mon.Monitor, disp))
	})
	return out
}

var (
	enumWindowsCallback uintptr
	enumWindowsSink     func(hwnd uintptr)
	enumWindowsBusy     bool
)

// enumWindows 用包级回调 + 包级 sink 枚举顶层窗口。
//
// syscall.NewCallback 每创建一个回调都会永久占用一个运行时槽位（数量有限），
// 所以回调只建一次；回调里的 panic 穿过 C 边界会直接终止进程，必须兜住。
// sink 是包级状态，因此不允许并发重入。
func enumWindows(fn func(hwnd uintptr)) {
	if enumWindowsBusy {
		return
	}
	enumWindowsBusy = true
	defer func() { enumWindowsBusy = false }()

	if enumWindowsCallback == 0 {
		enumWindowsCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("枚举截图候选窗口异常：%v", r)
				}
			}()
			if enumWindowsSink != nil {
				enumWindowsSink(hwnd)
			}
			return 1 // 继续枚举
		})
	}
	enumWindowsSink = fn
	defer func() { enumWindowsSink = nil }()
	procEnumWindows.Call(enumWindowsCallback, 0)
}

// windowCandidate 读取一个顶层窗口的属性。不可见的窗口直接返回零值，
// acceptWindow 会在 visible 上拒绝它，省掉后面的跨进程调用。
func windowCandidate(hwnd uintptr) winCand {
	var c winCand
	if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
		return c
	}
	c.visible = true
	if v, _, _ := procIsIconic.Call(hwnd); v != 0 {
		c.iconic = true
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	c.pid = pid
	c.exStyle, _, _ = procGetWindowLongPtrW.Call(hwnd, gwlExStyle)
	if n, _, _ := procGetWindowTextLengthW.Call(hwnd); n != 0 {
		c.titleLen = int(int32(n))
	}
	c.class = windowClassName(hwnd)
	c.cloaked = windowCloaked(hwnd)
	c.rect = windowFrame(hwnd)
	return c
}

func windowClassName(hwnd uintptr) string {
	var buf [128]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:int(n)])
}

// windowCloaked 判断窗口是否被 DWM 隐藏：其他虚拟桌面上的窗口、
// 被 shell 收起的 UWP 窗口仍然是「可见」的顶层窗口，只能靠这个属性排除。
func windowCloaked(hwnd uintptr) bool {
	var cloaked uint32
	if v, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaCloaked,
		uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked)); v != 0 {
		return false // 旧系统没有这个属性，按未隐藏处理
	}
	return cloaked != 0
}

// windowFrame 优先取 DWM 的扩展边框：Win10+ 的 GetWindowRect 会把窗口周围
// 不可见的阴影也计算进去，直接截会多出一圈透明边。
func windowFrame(hwnd uintptr) gdiRect {
	var r gdiRect
	if v, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaExtendedFrameBounds,
		uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)); v == 0 { // S_OK
		return r
	}
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

// pixelRectToDIP 把窗口的物理像素矩形换算成 mygo 的屏幕 DIP 坐标。
//
// 不能简单地除以 DPI：mygo 的 DIP 空间原点按全屏最小 DPI 计算，直接
// pixel*96/dpi 会有系统偏移。这里把该显示器的物理矩形线性映射到它自己的
// disp.Bounds，副屏负坐标与不同缩放比例都能对上；跨屏窗口超出本显示器的
// 部分映射后同样在 disp.Bounds 之外，交给 localWindowRects 裁掉。
func pixelRectToDIP(r gdiRect, monPx gdiRect, disp mygo.Display) mygo.Rectangle {
	mw := monPx.Right - monPx.Left
	mh := monPx.Bottom - monPx.Top
	if mw <= 0 || mh <= 0 {
		return mygo.Rectangle{}
	}
	sx := float64(disp.Bounds.Width) / float64(mw)
	sy := float64(disp.Bounds.Height) / float64(mh)
	return mygo.Rectangle{
		X:      disp.Bounds.X + int(math.Round(float64(r.Left-monPx.Left)*sx)),
		Y:      disp.Bounds.Y + int(math.Round(float64(r.Top-monPx.Top)*sy)),
		Width:  int(math.Round(float64(r.Right-r.Left) * sx)),
		Height: int(math.Round(float64(r.Bottom-r.Top) * sy)),
	}
}
