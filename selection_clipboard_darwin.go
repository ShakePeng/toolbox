//go:build darwin

package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// 当 AX 读不到选区时的兜底：临时模拟一次 ⌘C，从剪贴板取回文本。
// 读取前备份完整剪贴板，读取后恢复，尽量不影响用户原有内容。
const (
	coreGraphicsPath     = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	cgHIDEventTap        = 0
	cgEventFlagMaskCmd   = 1 << 20
	keyCodeC             = 0x08 // ANSI 键盘布局里的 C
	clipboardPollTimeout = 700 * time.Millisecond
)

var (
	cgEventOnce sync.Once
	cgEventErr  error

	cgEventCreateKeyboardEvent func(source uintptr, virtualKey uint16, keyDown bool) uintptr
	cgEventSetFlags            func(event uintptr, flags uint64)
	cgEventPost                func(tap uint32, event uintptr)
)

func loadCGEvent() error {
	cgEventOnce.Do(func() {
		lib, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			cgEventErr = fmt.Errorf("加载 CoreGraphics 失败：%w", err)
			return
		}
		purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, lib, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventSetFlags, lib, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPost, lib, "CGEventPost")
	})
	return cgEventErr
}

// copySelectionViaClipboard 发送 ⌘C 并从剪贴板读取选区。
func copySelectionViaClipboard() (text string, err error) {
	// 先备份完整剪贴板。读取失败时不冒险覆盖用户内容。
	old, readErr := mygo.Clipboard.Read()
	if readErr != nil {
		return "", fmt.Errorf("无法备份剪贴板：%w", readErr)
	}

	// 写入一个唯一标记，用来区分“没有复制到任何内容”和“复制到了旧剪贴板内容”。
	marker := fmt.Sprintf("__toolbox_selection_%d__", time.Now().UnixNano())
	mygo.Clipboard.WriteText(marker)
	defer func() {
		if restoreErr := mygo.Clipboard.Write(old); restoreErr != nil {
			log.Printf("读取选区：恢复剪贴板失败：%v", restoreErr)
		}
	}()

	if err := postCommandC(); err != nil {
		return "", err
	}

	deadline := time.Now().Add(clipboardPollTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
		s := strings.TrimSpace(mygo.Clipboard.ReadText())
		// 标记还在，或目标应用把选区复制成了空内容，都继续等。
		if s == "" || s == marker {
			continue
		}
		return s, nil
	}
	return "", errors.New("没有检测到选中的文本")
}

// postCommandC 向系统发送一次 ⌘C 的按下与抬起事件。
func postCommandC() error {
	if err := loadCGEvent(); err != nil {
		return err
	}
	down := cgEventCreateKeyboardEvent(0, keyCodeC, true)
	up := cgEventCreateKeyboardEvent(0, keyCodeC, false)
	if down == 0 || up == 0 {
		return errors.New("无法创建复制按键事件")
	}
	cgEventSetFlags(down, cgEventFlagMaskCmd)
	cgEventSetFlags(up, cgEventFlagMaskCmd)
	cgEventPost(cgHIDEventTap, down)
	cgEventPost(cgHIDEventTap, up)
	// cfRelease 由 AX 初始化绑定；两者都依赖同一个 CoreFoundation。
	if cfRelease != nil {
		cfRelease(down)
		cfRelease(up)
	}
	return nil
}
