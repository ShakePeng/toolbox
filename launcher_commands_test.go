package main

import (
	"testing"
)

// 内置命令的匹配规则：带参数时只允许前缀，不带参数时保留子串；
// 少于两个字符不提示，避免单字母抢走应用和文件结果。
func TestMatchBuiltinCommand(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  builtinCommandID
		arg   string
		ok    bool
	}{
		{"rei", cmdReindex, "", true},
		{"重建", cmdReindex, "", true},
		{"索引", cmdReindex, "", true},
		{"reindex now", cmdReindex, "now", true},
		// tiny 已移出内置命令表，改由 ImageCompressSettings.Command 配置匹配。
		{"tin", builtinCommandID(""), "", false},
		{"tiny", builtinCommandID(""), "", false},
		{"图片", builtinCommandID(""), "", false},
		{"压缩", builtinCommandID(""), "", false},
		{"set", cmdSettings, "", true},
		{"设置", cmdSettings, "", true},
		{"退出", cmdQuit, "", true},
		// 截图、翻译只走快捷键，不是内置命令，输入命令词不应命中。
		{"截图", builtinCommandID(""), "", false},
		{"翻译 你好", builtinCommandID(""), "", false},
		{"v", builtinCommandID(""), "", false},
		{"u", builtinCommandID(""), "", false},
		{"foo", builtinCommandID(""), "", false},
	} {
		cmd, arg, ok := matchBuiltinCommand(tc.query)
		if ok != tc.ok {
			t.Fatalf("matchBuiltinCommand(%q) 命中=%v，期望 %v", tc.query, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if cmd.ID != tc.want {
			t.Errorf("matchBuiltinCommand(%q) 命中 %q，期望 %q", tc.query, cmd.ID, tc.want)
		}
		if arg != tc.arg {
			t.Errorf("matchBuiltinCommand(%q) 参数 %q，期望 %q", tc.query, arg, tc.arg)
		}
	}
}

// 触发词冲突校验只认完整命令名，输错一个字不应被当成冲突。
func TestIsBuiltinCommandExact(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"reindex", true},
		{"REINDEX", true},
		{" 重建索引 ", true},
		{"reindex2", false},
		// 截图、翻译已移出内置命令，不再占用触发词。
		{"截图", false},
		{"翻译", false},
		{"", false},
	} {
		if got := isBuiltinCommand(tc.name); got != tc.want {
			t.Errorf("isBuiltinCommand(%q) = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// 每条命令的声明都要完整：至少一个名字、能查到的图标、可执行的 Run。
func TestBuiltinCommandTableWellFormed(t *testing.T) {
	seen := make(map[builtinCommandID]bool)
	for _, cmd := range builtinCommandTable() {
		if cmd.ID == "" || seen[cmd.ID] {
			t.Errorf("命令 ID 为空或重复：%q", cmd.ID)
		}
		seen[cmd.ID] = true
		if len(cmd.Names) == 0 || cmd.Names[0] == "" {
			t.Errorf("命令 %q 缺少名字", cmd.ID)
		}
		if cmd.Title == "" || cmd.Desc == "" {
			t.Errorf("命令 %q 缺少标题或说明", cmd.ID)
		}
		if _, ok := iconSVG[cmd.Icon]; !ok {
			t.Errorf("命令 %q 引用了不存在的图标 %q", cmd.ID, cmd.Icon)
		}
		if cmd.Run == nil {
			t.Errorf("命令 %q 没有 Run", cmd.ID)
		}
		if _, ok := lookupBuiltinCommand(cmd.ID); !ok {
			t.Errorf("命令 %q 无法按 ID 查回", cmd.ID)
		}
	}
}

// 搜索触发词与内置命令重名时必须被拦下，否则输入会被命令截走。
func TestBuiltinCommandBlocksTriggerWord(t *testing.T) {
	a := newTestApp()
	if got := a.webSearchCommandConflict("baidu", "reindex"); got == "" {
		t.Error("搜索触发词与内置命令重名时应提示冲突")
	}
	if got := a.projectCommandConflict("reindex"); got == "" {
		t.Error("项目触发词与内置命令重名时应提示冲突")
	}
	if got := a.projectCommandConflict("code"); got != "" {
		t.Errorf("正常触发词不应报冲突，实际 %q", got)
	}
}
