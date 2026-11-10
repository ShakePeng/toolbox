//go:build darwin

package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo/plugins/permissionflow"
)

// 通过 macOS 辅助功能（Accessibility）读取当前焦点元素里被选中的文本。
// 这是划词翻译的核心：不模拟 Cmd+C，因此不会改动用户的剪贴板。
const (
	applicationServicesPath = "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"
	coreFoundationPath      = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	kCFStringEncodingUTF8   = 0x08000100
	kAXErrorSuccess         = 0
)

// AXError 的常见取值，用来把系统错误翻译成可读提示。
const (
	kAXErrorAPIDisabled          = -25211
	kAXErrorCannotComplete       = -25204
	kAXErrorAttributeUnsupported = -25205
	kAXErrorInvalidUIElement     = -25212
)

var (
	axOnce sync.Once
	axErr  error
	// cfBooleanTrue 是 CoreFoundation 的 kCFBooleanTrue，用于给第三方应用
	// 打开辅助功能树（Electron/Chromium 需要显式触发）。
	cfBooleanTrue uintptr

	axUIElementCreateSystemWide       func() uintptr
	axUIElementCreateApplication      func(pid int32) uintptr
	axUIElementCopyAttributeValue     func(element uintptr, attribute uintptr, value *uintptr) int32
	axUIElementSetAttributeValue      func(element uintptr, attribute uintptr, value uintptr) int32
	axValueGetValue                   func(value uintptr, theType uint32, out unsafe.Pointer) bool
	cfStringCreateWithCString         func(alloc uintptr, cStr *byte, encoding uint32) uintptr
	cfStringGetLength                 func(s uintptr) int64
	cfStringGetMaximumSizeForEncoding func(length int64, encoding uint32) int64
	cfStringGetCString                func(s uintptr, buffer *byte, bufferSize int64, encoding uint32) bool
	cfBooleanGetValue                 func(ref uintptr) bool
	cfRelease                         func(obj uintptr)
)

// loadAX 只在第一次调用时加载框架并绑定函数，避免每次划词都做动态链接。
func loadAX() error {
	axOnce.Do(func() {
		services, err := purego.Dlopen(applicationServicesPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			axErr = fmt.Errorf("加载 ApplicationServices 失败：%w", err)
			return
		}
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			axErr = fmt.Errorf("加载 CoreFoundation 失败：%w", err)
			return
		}
		// AXUIElementSetAttributeValue 需要 CFBooleanRef。用 NSNumber 的
		// numberWithBool: 取一个 toll-free bridged 的 __NSCFBoolean，
		// 避免直接读 kCFBooleanTrue 导出变量的地址（需要 unsafe）。
		if cls := objc.GetClass("NSNumber"); cls != 0 {
			// numberWithBool: 返回 __NSCFBoolean 单例，与 CFBooleanRef 互通，
			// 也是 AX 属性设置期望的规范布尔对象。
			n := objc.ID(cls).Send(objc.RegisterName("numberWithBool:"), true)
			cfBooleanTrue = uintptr(n)
		}
		for _, f := range []struct {
			fn        any
			lib       uintptr
			name      string
			framework string
		}{
			{&axUIElementCreateSystemWide, services, "AXUIElementCreateSystemWide", "Accessibility"},
			{&axUIElementCreateApplication, services, "AXUIElementCreateApplication", "Accessibility"},
			{&axUIElementCopyAttributeValue, services, "AXUIElementCopyAttributeValue", "Accessibility"},
			{&axUIElementSetAttributeValue, services, "AXUIElementSetAttributeValue", "Accessibility"},
			{&axValueGetValue, services, "AXValueGetValue", "Accessibility"},
			{&cfStringCreateWithCString, cf, "CFStringCreateWithCString", "CoreFoundation"},
			{&cfStringGetLength, cf, "CFStringGetLength", "CoreFoundation"},
			{&cfStringGetMaximumSizeForEncoding, cf, "CFStringGetMaximumSizeForEncoding", "CoreFoundation"},
			{&cfStringGetCString, cf, "CFStringGetCString", "CoreFoundation"},
			{&cfBooleanGetValue, cf, "CFBooleanGetValue", "CoreFoundation"},
			{&cfRelease, cf, "CFRelease", "CoreFoundation"},
		} {
			p, err := purego.Dlsym(f.lib, f.name)
			if err != nil {
				axErr = fmt.Errorf("缺少 %s（%s）：%w", f.name, f.framework, err)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
	})
	return axErr
}

// 这两类结果是“确实没有选区”，不应该再改用剪贴板方式，
// 否则在支持 AX 的应用里按 Cmd+C 可能把整行甚至其他内容复制进来。
var (
	errNoSelection      = errors.New("没有检测到选中的文本")
	errNoFocusedElement = errors.New("当前应用没有焦点元素")
	// errSelectionNotExposed 表示应用知道有选区，但没通过 AXSelectedText 暴露文字。
	// 这种情况可以安全地改用剪贴板方式读取。
	errSelectionNotExposed = errors.New("选区文字未暴露")
)

// readSelectedText 返回当前前台应用中选中的文本。
// 优先用 AX 直接读；应用不暴露选区或元素失效时，退回剪贴板方式。
func readSelectedText() (string, error) {
	// 两种方式都依赖辅助功能授权。
	if !permissionflow.Allowed(permissionflow.Accessibility) {
		return "", errors.New("未获得辅助功能权限")
	}
	text, err := readSelectedTextAX()
	if err == nil {
		return text, nil
	}
	// 只有“确实没有焦点元素”不兜底；空选区也交给剪贴板方式，
	// 与 S-Tools 的行为一致（部分应用不会把选区同步到 AX）。
	if errors.Is(err, errNoFocusedElement) {
		return "", err
	}
	log.Printf("读取选区：AX 方式失败，改用剪贴板方式：%v", err)
	text, cbErr := copySelectionViaClipboard()
	if cbErr == nil {
		return text, nil
	}
	log.Printf("读取选区：剪贴板方式也失败：%v", cbErr)
	return "", err
}

// readSelectedTextAX 用 macOS 辅助功能读取当前焦点元素里的选中文本。
func readSelectedTextAX() (string, error) {
	if err := loadAX(); err != nil {
		return "", err
	}

	system := axUIElementCreateSystemWide()
	if system == 0 {
		return "", errors.New("无法创建辅助功能元素")
	}
	defer cfRelease(system)

	focused, err := axFocusedElement(system)
	if err != nil {
		return "", fmt.Errorf("读取当前焦点元素失败：%w", err)
	}
	if focused == 0 {
		return "", errNoFocusedElement
	}
	// 用闭包捕获变量本身：重试时 focused 会被替换，defer cfRelease(focused)
	// 会在 defer 语句处就求值，导致旧元素被手动释放后再被释放一次（double free）。
	defer func() { cfRelease(focused) }()

	value, err := axCopyAttribute(focused, "AXSelectedText")
	if err == nil && value == 0 {
		// 属性存在但为空，等同于没有选中文本。
		return "", errNoSelection
	}
	if err != nil {
		// 焦点切换的瞬间，部分应用会短暂返回失效元素；重新取一次焦点元素再试，
		// 避免用户刚切回目标应用就按快捷键时误报。
		time.Sleep(30 * time.Millisecond)
		refetched, ferr := axFocusedElement(system)
		if ferr != nil || refetched == 0 {
			return "", fmt.Errorf("当前应用不支持读取选区：%w", err)
		}
		cfRelease(focused)
		focused = refetched
		value, err = axCopyAttribute(focused, "AXSelectedText")
		if err != nil {
			log.Printf("读取选区：AXSelectedText 失败：%v（前台 pid=%d）", err, frontmostPID())
			return "", fmt.Errorf("当前应用不支持读取选区：%w", err)
		}
	}
	if value == 0 {
		if axSelectionLength(focused) > 0 {
			return "", errSelectionNotExposed
		}
		return "", errNoSelection
	}
	defer cfRelease(value)

	text, err := cfStringToGo(value)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		if axSelectionLength(focused) > 0 {
			return "", errSelectionNotExposed
		}
		return "", errNoSelection
	}
	return text, nil
}

// cfRange 对应 CoreFoundation 的 CFRange（两个 CFIndex）。
type cfRange struct {
	Location int64
	Length   int64
}

// kAXValueTypeCFRange 是 AXValueGetValue 用的 CFRange 类型。
const kAXValueTypeCFRange = 4

// axSelectionLength 从 AXSelectedTextRange 读选区长度。
// 有些应用（如 VS Code 的部分视图）只暴露范围，不暴露 AXSelectedText。
func axSelectionLength(element uintptr) int64 {
	if axValueGetValue == nil {
		return 0
	}
	attr, err := cfString("AXSelectedTextRange")
	if err != nil {
		return 0
	}
	defer cfRelease(attr)

	var value uintptr
	if code := axUIElementCopyAttributeValue(element, attr, &value); code != 0 || value == 0 {
		return 0
	}
	defer cfRelease(value)

	var r cfRange
	if !axValueGetValue(value, kAXValueTypeCFRange, unsafe.Pointer(&r)) {
		return 0
	}
	return r.Length
}

// axFocusedElement 依次尝试三条路径，尽量避开“元素已失效”的瞬时状态：
// 1. 系统级元素直接要焦点元素
// 2. 系统级元素拿焦点应用，再从该应用取焦点元素
// 3. 按前台进程 PID 新建应用元素，再取焦点元素
func axFocusedElement(system uintptr) (uintptr, error) {
	focused, err := axCopyAttribute(system, "AXFocusedUIElement")
	if err == nil && focused != 0 {
		return focused, nil
	}
	lastErr := err

	if app, aerr := axCopyAttribute(system, "AXFocusedApplication"); aerr == nil && app != 0 {
		f, ferr := axCopyAttribute(app, "AXFocusedUIElement")
		cfRelease(app)
		if ferr == nil && f != 0 {
			return f, nil
		}
		if ferr != nil {
			lastErr = ferr
		}
	} else if aerr != nil {
		lastErr = aerr
	}

	if pid := frontmostPID(); pid > 0 && axUIElementCreateApplication != nil {
		app := axUIElementCreateApplication(pid)
		if app != 0 {
			// Electron / Chromium 应用默认不构建辅助功能树，先显式打开。
			// 首次打开是异步的，要等树构建完；已打开的应用直接查询。
			if !accessibilityTreeEnabled(app) {
				if enableAccessibilityTree(app) {
					time.Sleep(250 * time.Millisecond)
				}
			}
			f, ferr := axCopyAttribute(app, "AXFocusedUIElement")
			cfRelease(app)
			if ferr == nil && f != 0 {
				return f, nil
			}
			if ferr != nil {
				lastErr = ferr
			}
		}
	}
	return 0, lastErr
}

// accessibilityTreeEnabled 读取 AXManualAccessibility，判断第三方应用是否
// 已经打开了辅助功能树。原生应用没有这个属性，返回 false。
func accessibilityTreeEnabled(app uintptr) bool {
	attr, err := cfString("AXManualAccessibility")
	if err != nil {
		return false
	}
	defer cfRelease(attr)
	var value uintptr
	if code := axUIElementCopyAttributeValue(app, attr, &value); code != 0 || value == 0 {
		return false
	}
	defer cfRelease(value)
	// 不能用对象指针比较：我们自己创建的 NSNumber 与系统返回的
	// CFBoolean 不保证是同一个实例，应按布尔值判断。
	if cfBooleanGetValue != nil {
		return cfBooleanGetValue(value)
	}
	return value == cfBooleanTrue
}

// enableAccessibilityTree 为第三方应用打开辅助功能树。
// Electron（VS Code、Slack 等）用 AXManualAccessibility，Chromium 系还认
// AXEnhancedUserInterface。返回是否至少有一个属性设置成功。
func enableAccessibilityTree(app uintptr) bool {
	if axUIElementSetAttributeValue == nil || cfBooleanTrue == 0 {
		return false
	}
	enabled := false
	for _, name := range []string{"AXManualAccessibility", "AXEnhancedUserInterface"} {
		attr, err := cfString(name)
		if err != nil {
			continue
		}
		code := axUIElementSetAttributeValue(app, attr, cfBooleanTrue)
		cfRelease(attr)
		if code == 0 {
			log.Printf("AX: 已启用 %s", name)
			enabled = true
		}
	}
	return enabled
}

// frontmostPID 通过 NSWorkspace 取当前前台应用的进程号。
// 走这条路径不依赖 system-wide 元素，某些应用下更稳定。
func frontmostPID() int32 {
	cls := objc.GetClass("NSWorkspace")
	if cls == 0 {
		return 0
	}
	ws := objc.ID(cls).Send(objc.RegisterName("sharedWorkspace"))
	if ws == 0 {
		return 0
	}
	front := ws.Send(objc.RegisterName("frontmostApplication"))
	if front == 0 {
		return 0
	}
	return objc.Send[int32](front, objc.RegisterName("processIdentifier"))
}

// axCopyAttribute 调用 AXUIElementCopyAttributeValue，并负责释放属性名。
func axCopyAttribute(element uintptr, name string) (uintptr, error) {
	attr, err := cfString(name)
	if err != nil {
		return 0, err
	}
	defer cfRelease(attr)

	var value uintptr
	code := axUIElementCopyAttributeValue(element, attr, &value)
	if code != kAXErrorSuccess {
		return 0, axErrorText(code)
	}
	return value, nil
}

// cfString 把 Go 字符串转成 CFStringRef，调用方负责 CFRelease。
func cfString(s string) (uintptr, error) {
	// CFStringCreateWithCString 需要以 NUL 结尾的字节串。
	// 指针以 uintptr 传给 C，GC 不会把它当成引用；必须用 KeepAlive 保证
	// 调用期间字节串不被回收或移动，否则会构造出非法的 CFString。
	p, err := syscall.BytePtrFromString(s)
	if err != nil {
		return 0, fmt.Errorf("创建 CFString 失败：%w", err)
	}
	ref := cfStringCreateWithCString(0, p, kCFStringEncodingUTF8)
	runtime.KeepAlive(p)
	if ref == 0 {
		return 0, fmt.Errorf("创建 CFString 失败：%s", s)
	}
	return ref, nil
}

// cfStringToGo 把 CFStringRef 按 UTF-8 取出为 Go 字符串。
func cfStringToGo(ref uintptr) (string, error) {
	n := cfStringGetLength(ref)
	size := cfStringGetMaximumSizeForEncoding(n, kCFStringEncodingUTF8) + 1
	if size <= 1 {
		return "", nil
	}
	buf := make([]byte, size)
	ok := cfStringGetCString(ref, &buf[0], size, kCFStringEncodingUTF8)
	// 同上：传给 C 的是 uintptr，需要显式保证缓冲区在调用期间存活。
	runtime.KeepAlive(buf)
	if !ok {
		return "", errors.New("转换选中文本编码失败")
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf), nil
}

func axErrorText(code int32) error {
	switch code {
	case kAXErrorAPIDisabled:
		return errors.New("辅助功能权限未开启")
	case kAXErrorAttributeUnsupported:
		return errors.New("该应用未暴露选区")
	case kAXErrorCannotComplete:
		return fmt.Errorf("系统暂时无法完成请求（%d）", code)
	case kAXErrorInvalidUIElement:
		return fmt.Errorf("焦点元素已失效（%d）", code)
	}
	return fmt.Errorf("辅助功能错误（%d）", code)
}
