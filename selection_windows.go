//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
)

// Windows 没有统一的跨应用选区接口（UI Automation 对终端、部分 Electron
// 应用并不可靠），因此和对 macOS 的兜底一致：模拟一次 Ctrl+C，从剪贴板
// 取回文本，读完恢复原剪贴板。这也是 S-Tools 在 Windows 上的做法。
const (
	vkShift        = 0x10
	vkControl      = 0x11
	vkMenu         = 0x12
	vkLWin         = 0x5B
	vkRWin         = 0x5C
	vkC            = 0x43
	inputKeyboard  = 1
	keyEventFKeyUp = 0x0002

	clipboardPollTimeout = 700 * time.Millisecond
	clipboardRetryWindow = 300 * time.Millisecond
)

// winKeybdInput 对应 KEYBDINPUT。ExtraInfo 用 uintptr 保证 64 位布局。
type winKeybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// winInput 对应 INPUT。Type 后补 4 字节，再加 MOUSEINPUT 比 KEYBDINPUT
// 多出的 8 字节，使整个结构在 64 位 Windows 上正好是 40 字节。
type winInput struct {
	Type uint32
	_    uint32
	Ki   winKeybdInput
	_    [8]byte
}

var (
	// 独立加载一份 user32，避免选区读取依赖截图文件里的全局变量。
	user32InputDLL       = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32InputDLL.NewProc("GetAsyncKeyState")
	procSendInput        = user32InputDLL.NewProc("SendInput")
)

// readSelectedText 通过剪贴板取回当前焦点应用里选中的文本。
func readSelectedText() (string, error) {
	return copySelectionViaClipboardWindows()
}

func copySelectionViaClipboardWindows() (text string, err error) {
	// 快捷键可能还按着 Alt，先等修饰键抬起，否则发出去的是 Ctrl+Alt+C。
	waitForModifiersReleased(300 * time.Millisecond)

	// 先完整备份剪贴板；读取失败时宁可不复制，也不覆盖用户原有内容。
	old, readErr := mygo.Clipboard.Read()
	if readErr != nil {
		return "", fmt.Errorf("无法备份剪贴板：%w", readErr)
	}

	// 写入唯一标记，用来区分“没有复制到内容”和“复制到了旧剪贴板内容”。
	marker := fmt.Sprintf("__toolbox_selection_%d__", time.Now().UnixNano())
	mygo.Clipboard.WriteText(marker)
	defer func() {
		if restoreErr := mygo.Clipboard.Write(old); restoreErr != nil {
			log.Printf("读取选区：恢复剪贴板失败：%v", restoreErr)
		}
	}()

	// WriteText 不返回错误；确认标记真的写进去了，否则剪贴板里的旧内容
	// 会被误当成刚复制到的选区。
	if strings.TrimSpace(mygo.Clipboard.ReadText()) != marker {
		return "", errors.New("无法写入剪贴板，请稍后重试")
	}

	if !sendCtrlC() {
		return "", errors.New("无法发送复制快捷键")
	}
	text = waitForClipboardText(marker, clipboardPollTimeout)
	if text == "" {
		// 某些应用第一次 Ctrl+C 只用来把焦点从菜单/输入法切回正文，再试一次。
		time.Sleep(80 * time.Millisecond)
		if sendCtrlC() {
			text = waitForClipboardText(marker, clipboardRetryWindow)
		}
	}
	if text == "" {
		return "", errors.New("没有检测到选中的文本")
	}
	return text, nil
}

func waitForClipboardText(marker string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
		s := strings.TrimSpace(mygo.Clipboard.ReadText())
		// 标记还在，或目标应用把选区复制成了空内容，都继续等。
		if s == "" || s == marker {
			continue
		}
		return s
	}
	return ""
}

func waitForModifiersReleased(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pressed := false
		for _, vk := range []uintptr{vkMenu, vkControl, vkShift, vkLWin, vkRWin} {
			state, _, _ := procGetAsyncKeyState.Call(vk)
			if int16(state) < 0 {
				pressed = true
				break
			}
		}
		if !pressed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sendCtrlC() bool {
	inputs := [4]winInput{
		{Type: inputKeyboard, Ki: winKeybdInput{Vk: vkControl}},
		{Type: inputKeyboard, Ki: winKeybdInput{Vk: vkC}},
		{Type: inputKeyboard, Ki: winKeybdInput{Vk: vkC, Flags: keyEventFKeyUp}},
		{Type: inputKeyboard, Ki: winKeybdInput{Vk: vkControl, Flags: keyEventFKeyUp}},
	}
	sent, _, _ := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	return sent == uintptr(len(inputs))
}
