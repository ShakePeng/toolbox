//go:build darwin

package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"syscall"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// 截图悬停选窗的 macOS 实现：CGWindowListCopyWindowInfo 一次拿到当前空间里
// 所有在屏窗口的边界、层级和 PID。
//
// 这里刻意自带一份最小绑定，不复用 selection_darwin.go 里 loadAX 的全局函数：
// 那张表是惰性初始化的，用户没划过词时这些函数指针还是 nil，截图直接调用会 panic。
const (
	libobjcPath = "/usr/lib/libobjc.A.dylib"

	kCGWindowListOptionOnScreenOnly     = 1 << 0
	kCGWindowListExcludeDesktopElements = 1 << 4
)

// CGWindowListCopyWindowInfo 返回字典里用到的键。CFString 与 NSString
// toll-free bridged，直接拿去给 NSDictionary 的 objectForKey: 用。
const (
	pickKeyPID = iota
	pickKeyLayer
	pickKeyAlpha
	pickKeyBounds
	pickKeyX
	pickKeyY
	pickKeyW
	pickKeyH
	pickKeyCount
)

var pickKeyNames = [pickKeyCount]string{
	"kCGWindowOwnerPID", "kCGWindowLayer", "kCGWindowAlpha", "kCGWindowBounds",
	"X", "Y", "Width", "Height",
}

var (
	pickOnce sync.Once
	pickErr  error

	pickCGWindowList   func(option, relativeTo uint32) uintptr
	pickCFCreateString func(alloc uintptr, cStr *byte, encoding uint32) uintptr
	pickCFRelease      func(obj uintptr)

	// objc_msgSend 要按返回类型用不同签名调用，预注册三种避免每次调用
	// 都走 objc.Send（它内部每次都会新建一个 trampoline）。
	pickSendUint  func(obj objc.ID, sel objc.SEL) uint
	pickSendInt   func(obj objc.ID, sel objc.SEL) int
	pickSendFloat func(obj objc.ID, sel objc.SEL) float64

	pickSelCount         = objc.RegisterName("count")
	pickSelObjectAtIndex = objc.RegisterName("objectAtIndex:")
	pickSelObjectForKey  = objc.RegisterName("objectForKey:")
	pickSelIntegerValue  = objc.RegisterName("integerValue")
	pickSelDoubleValue   = objc.RegisterName("doubleValue")
)

// loadPickWindows 只在第一次截图时加载框架并绑定函数。
func loadPickWindows() error {
	pickOnce.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			pickErr = fmt.Errorf("加载 CoreGraphics 失败：%w", err)
			return
		}
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			pickErr = fmt.Errorf("加载 CoreFoundation 失败：%w", err)
			return
		}
		objcLib, err := purego.Dlopen(libobjcPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			pickErr = fmt.Errorf("加载 libobjc 失败：%w", err)
			return
		}
		for _, f := range []struct {
			fn   any
			lib  uintptr
			name string
		}{
			{&pickCGWindowList, cg, "CGWindowListCopyWindowInfo"},
			{&pickCFCreateString, cf, "CFStringCreateWithCString"},
			{&pickCFRelease, cf, "CFRelease"},
		} {
			p, err := purego.Dlsym(f.lib, f.name)
			if err != nil {
				pickErr = fmt.Errorf("缺少 %s：%w", f.name, err)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
		msgSend, err := purego.Dlsym(objcLib, "objc_msgSend")
		if err != nil {
			pickErr = fmt.Errorf("缺少 objc_msgSend：%w", err)
			return
		}
		purego.RegisterFunc(&pickSendUint, msgSend)
		purego.RegisterFunc(&pickSendInt, msgSend)
		purego.RegisterFunc(&pickSendFloat, msgSend)
	})
	return pickErr
}

// pickCFString 把 Go 字符串转成 CFStringRef，调用方负责 CFRelease。
func pickCFString(s string) (uintptr, error) {
	// 指针以 uintptr 传给 C，GC 不会把它当成引用；必须 KeepAlive，
	// 否则调用期间字节串可能被回收或移动。
	p, err := syscall.BytePtrFromString(s)
	if err != nil {
		return 0, err
	}
	ref := pickCFCreateString(0, p, kCFStringEncodingUTF8)
	runtime.KeepAlive(p)
	if ref == 0 {
		return 0, fmt.Errorf("创建 CFString 失败：%s", s)
	}
	return ref, nil
}

// listCaptureWindowsPlatform 返回当前空间里在屏应用窗口的屏幕 DIP 矩形，
// 顺序即 z 序（从前到后）。kCGWindowBounds 本身就是「主屏左上原点、
// y 向下、逻辑点」，与 mygo 的坐标一致，不需要翻转或缩放。
func listCaptureWindowsPlatform(_ mygo.Display) []mygo.Rectangle {
	if err := loadPickWindows(); err != nil {
		return nil
	}
	list := pickCGWindowList(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements, 0)
	if list == 0 {
		return nil
	}
	// Copy 规则：这个数组由我们持有，必须释放。
	defer pickCFRelease(list)

	// Create 规则：键由我们创建、也必须由我们释放。
	keys := make([]uintptr, pickKeyCount)
	defer func() {
		for _, k := range keys {
			if k != 0 {
				pickCFRelease(k)
			}
		}
	}()
	for i, name := range pickKeyNames {
		k, err := pickCFString(name)
		if err != nil {
			return nil
		}
		keys[i] = k
	}

	self := int32(os.Getpid())
	n := int(pickSendUint(objc.ID(list), pickSelCount))
	out := make([]mygo.Rectangle, 0, n)
	for i := 0; i < n; i++ {
		// CFArray / CFDictionary 与 NSArray / NSDictionary toll-free bridged，
		// 直接发消息取值即可。取出来的对象都是 borrowed，绝不能释放。
		w := objc.ID(list).Send(pickSelObjectAtIndex, i)
		if w == 0 {
			continue
		}
		if v := w.Send(pickSelObjectForKey, keys[pickKeyPID]); v == 0 ||
			int32(pickSendInt(v, pickSelIntegerValue)) == self {
			continue
		}
		// 只保留普通应用窗口层：菜单栏（24）、Dock（20）等系统 UI 都在别的层。
		if v := w.Send(pickSelObjectForKey, keys[pickKeyLayer]); v == 0 ||
			pickSendInt(v, pickSelIntegerValue) != 0 {
			continue
		}
		if v := w.Send(pickSelObjectForKey, keys[pickKeyAlpha]); v != 0 &&
			pickSendFloat(v, pickSelDoubleValue) <= 0.01 {
			continue
		}
		bounds := w.Send(pickSelObjectForKey, keys[pickKeyBounds])
		if bounds == 0 {
			continue
		}
		num := func(key uintptr) float64 {
			v := bounds.Send(pickSelObjectForKey, key)
			if v == 0 {
				return 0
			}
			return pickSendFloat(v, pickSelDoubleValue)
		}
		out = append(out, mygo.Rectangle{
			X:      int(math.Round(num(keys[pickKeyX]))),
			Y:      int(math.Round(num(keys[pickKeyY]))),
			Width:  int(math.Round(num(keys[pickKeyW]))),
			Height: int(math.Round(num(keys[pickKeyH]))),
		})
	}
	return out
}
