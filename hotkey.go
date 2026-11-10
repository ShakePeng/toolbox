package main

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

// keyNames 把物理按键映射成 accelerator 里的按键名。
// 只收录常用按键，够覆盖划词翻译/截图这类快捷键即可。
var keyNames = map[ui.Key]string{
	ui.KeyA: "A", ui.KeyB: "B", ui.KeyC: "C", ui.KeyD: "D", ui.KeyE: "E",
	ui.KeyF: "F", ui.KeyG: "G", ui.KeyH: "H", ui.KeyI: "I", ui.KeyJ: "J",
	ui.KeyK: "K", ui.KeyL: "L", ui.KeyM: "M", ui.KeyN: "N", ui.KeyO: "O",
	ui.KeyP: "P", ui.KeyQ: "Q", ui.KeyR: "R", ui.KeyS: "S", ui.KeyT: "T",
	ui.KeyU: "U", ui.KeyV: "V", ui.KeyW: "W", ui.KeyX: "X", ui.KeyY: "Y",
	ui.KeyZ: "Z",
	ui.Key0: "0", ui.Key1: "1", ui.Key2: "2", ui.Key3: "3", ui.Key4: "4",
	ui.Key5: "5", ui.Key6: "6", ui.Key7: "7", ui.Key8: "8", ui.Key9: "9",
	ui.KeyF1: "F1", ui.KeyF2: "F2", ui.KeyF3: "F3", ui.KeyF4: "F4",
	ui.KeyF5: "F5", ui.KeyF6: "F6", ui.KeyF7: "F7", ui.KeyF8: "F8",
	ui.KeyF9: "F9", ui.KeyF10: "F10", ui.KeyF11: "F11", ui.KeyF12: "F12",
	ui.KeySpace: "Space", ui.KeyMinus: "-", ui.KeyEqual: "=",
	ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/",
	ui.KeySemicolon: ";", ui.KeyQuote: "'",
	ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]",
	ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
}

// isFunctionKey 判断是否为功能键（功能键可单独作为快捷键）。
func isFunctionKey(name string) bool {
	if !strings.HasPrefix(name, "F") || len(name) < 2 {
		return false
	}
	n := 0
	for _, r := range name[1:] {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n >= 1 && n <= 12
}

// modPrefixes 把当前按下的修饰键转成 accelerator 前缀，顺序固定。
func modPrefixes(m ui.Modifiers) []string {
	var out []string
	// ui.Cmd 在 macOS 上是 Command，在 Windows/Linux 上等于 Ctrl。
	// 非 macOS 再输出 "Cmd" 会得到 Cmd+Ctrl+… 这种重复修饰键，且 "Cmd"
	// 会被解析成 Win 键，导致快捷键注册不上。
	if runtime.GOOS == "darwin" && m&ui.Cmd != 0 {
		out = append(out, "Cmd")
	}
	if m&ui.Ctrl != 0 {
		out = append(out, "Ctrl")
	}
	if m&ui.Alt != 0 {
		out = append(out, "Alt")
	}
	if m&ui.Shift != 0 {
		out = append(out, "Shift")
	}
	return out
}

// captureHotkey 在“录制”状态下，从当前按下的键里拼出一个 accelerator。
// ok 为 false 表示这一帧还没有凑齐合法的组合（例如只按了修饰键）。
func captureHotkey(c *ui.Context) (acc string, ok bool) {
	mods := modPrefixes(c.Modifiers())
	for _, k := range c.KeysDown() {
		name, known := keyNames[k]
		if !known {
			continue
		}
		// 普通按键必须有修饰键，功能键允许单独使用。
		if len(mods) == 0 && !isFunctionKey(name) {
			continue
		}
		return strings.Join(append(mods, name), "+"), true
	}
	return "", false
}

// formatHotkey 把 accelerator 格式化给界面显示：macOS 用符号，其他平台保留原样。
func formatHotkey(acc string) string {
	if acc == "" {
		return "未设置"
	}
	if runtime.GOOS != "darwin" {
		return acc
	}
	replacer := strings.NewReplacer(
		"CmdOrCtrl+", "⌘",
		"Cmd+", "⌘",
		"Ctrl+", "⌃",
		"Alt+", "⌥",
		"Shift+", "⇧",
	)
	return replacer.Replace(acc)
}
