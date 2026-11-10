//go:build windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
)

// Windows 没有 screencapture 这类命令行工具，直接用 GDI 把屏幕复制到位图。
// 进程由 mygo 设为 Per-Monitor V2 DPI 感知，因此 BitBlt 和 GetMonitorInfo
// 用的都是物理像素，和覆盖层按 DIP 缩放显示正好对上。
const (
	gdiSrcCopy        = 0x00CC0020 // SRCCOPY
	gdiDIBRGB         = 0          // DIB_RGB_COLORS
	gdiBitmapInfoSize = 40         // BITMAPINFOHEADER
)

type gdiRect struct {
	Left, Top, Right, Bottom int32
}

// gdiMonitorInfo 对应 MONITORINFO。用 cbSize=40 的短版本即可，
// 不需要 MONITORINFOEX 里的设备名。
type gdiMonitorInfo struct {
	Size    uint32
	Monitor gdiRect
	Work    gdiRect
	Flags   uint32
}

type gdiBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type gdiBitmapInfo struct {
	Header gdiBitmapInfoHeader
	Colors [1]uint32
}

var (
	user32DLL = syscall.NewLazyDLL("user32.dll")
	gdi32DLL  = syscall.NewLazyDLL("gdi32.dll")

	procGetDC           = user32DLL.NewProc("GetDC")
	procReleaseDC       = user32DLL.NewProc("ReleaseDC")
	procGetMonitorInfoW = user32DLL.NewProc("GetMonitorInfoW")

	procCreateCompatibleDC     = gdi32DLL.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32DLL.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32DLL.NewProc("SelectObject")
	procBitBlt                 = gdi32DLL.NewProc("BitBlt")
	procGetDIBits              = gdi32DLL.NewProc("GetDIBits")
	procDeleteObject           = gdi32DLL.NewProc("DeleteObject")
	procDeleteDC               = gdi32DLL.NewProc("DeleteDC")
)

// captureRegion 抓取 disp 对应的整块显示器。area 是 mygo 的 DIP 坐标，
// 只在 macOS 上需要；Windows 用显示器句柄取物理像素区域更可靠。
func captureRegion(disp mygo.Display, _ mygo.Rectangle) ([]byte, image.Image, error) {
	if disp.ID == 0 {
		return nil, nil, errors.New("无法识别当前显示器")
	}

	info := gdiMonitorInfo{Size: uint32(unsafe.Sizeof(gdiMonitorInfo{}))}
	ok, _, _ := procGetMonitorInfoW.Call(uintptr(disp.ID), uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return nil, nil, errors.New("无法获取显示器区域")
	}
	left, top := info.Monitor.Left, info.Monitor.Top
	width, height := info.Monitor.Right-left, info.Monitor.Bottom-top
	if width <= 0 || height <= 0 {
		return nil, nil, errors.New("显示器区域为空")
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, nil, errors.New("无法获取屏幕设备上下文")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, nil, errors.New("无法创建截图设备上下文")
	}
	defer procDeleteDC.Call(memDC)

	bitmap, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, nil, errors.New("无法创建截图位图")
	}
	// 先恢复原对象再删除位图，否则位图仍被 DC 占用，句柄会泄漏。
	old, _, _ := procSelectObject.Call(memDC, bitmap)
	defer func() {
		procSelectObject.Call(memDC, old)
		procDeleteObject.Call(bitmap)
	}()

	// 虚拟桌面坐标可能为负（副屏在左侧），BitBlt 支持负的源坐标。
	ok, _, _ = procBitBlt.Call(
		memDC, 0, 0, uintptr(width), uintptr(height),
		screenDC, uintptr(int64(left)), uintptr(int64(top)), gdiSrcCopy,
	)
	if ok == 0 {
		return nil, nil, errors.New("复制屏幕画面失败")
	}

	// GetDIBits 要求位图没有选入 DC，先把原位图选回去；defer 里再选一次
	// 是幂等的，保证失败路径也能恢复。
	procSelectObject.Call(memDC, old)

	bi := gdiBitmapInfo{
		Header: gdiBitmapInfoHeader{
			Size:        gdiBitmapInfoSize,
			Width:       width,
			Height:      -height, // 负数表示自上而下，省去翻转行序
			Planes:      1,
			BitCount:    32,
			Compression: 0,
		},
	}
	stride := int(width) * 4
	pixels := make([]byte, stride*int(height))
	ok, _, _ = procGetDIBits.Call(
		memDC, bitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bi)), gdiDIBRGB,
	)
	if ok == 0 {
		return nil, nil, errors.New("读取截图像素失败")
	}

	// GetDIBits 输出 BGRA，且 alpha 常常是 0；转成不透明的 NRGBA。
	img := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		src := pixels[y*stride:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < int(width); x++ {
			s, d := src[x*4:x*4+4], dst[x*4:x*4+4]
			d[0], d[1], d[2], d[3] = s[2], s[1], s[0], 0xFF
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, nil, fmt.Errorf("编码截图失败：%w", err)
	}
	return buf.Bytes(), img, nil
}
