package main

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
)

// 内置命令的稳定标识，只用于结果行的 UI key 和测试断言，不参与匹配。
type builtinCommandID string

const (
	cmdReindex  builtinCommandID = "reindex"
	cmdUpdate   builtinCommandID = "update"
	cmdAI       builtinCommandID = "ai"
	cmdSettings builtinCommandID = "settings"
	cmdQuit     builtinCommandID = "quit"
)

// builtinCommand 是启动器内置命令的唯一声明处。搜索、渲染、执行和设置页
// 都只读这张表，新增命令不需要再改五处 switch。
type builtinCommand struct {
	ID       builtinCommandID
	Names    []string // Names[0] 是规范名，其余是别名；全部参与匹配和冲突校验
	Title    string   // 结果行标题
	Desc     string   // 结果行副标题，也用于设置页说明
	Icon     string   // icons.go 里的图标名
	Keywords []string // 额外的模糊匹配词

	Run func(s *launcherSession, arg string) error

	// KeepOpen 执行后保留浮窗（如重建索引，跑完还要继续搜）。
	KeepOpen bool
}

// builtinCommandTable 返回全部内置命令。顺序决定同名命中时的优先级。
func builtinCommandTable() []builtinCommand {
	return []builtinCommand{
		{
			ID: cmdReindex, Names: []string{"reindex", "重建索引"},
			Title: "重建索引", Desc: "重新枚举已安装应用并重建文件索引", Icon: "refresh",
			Keywords: []string{"index", "索引", "刷新"},
			KeepOpen: true,
			Run: func(s *launcherSession, arg string) error {
				s.runReindexCommand()
				return nil
			},
		},
		{
			// 图片压缩不再是内置命令：触发词 tiny 移到 ImageCompressSettings.Command
			// 由用户配置（见 imageCompressCommand / launcherActions）。
			ID: cmdAI, Names: []string{"ai", "供应商", "AI供应商"},
			Title: "AI 供应商", Desc: "管理 OpenAI 兼容的供应商与模型", Icon: "provider",
			Keywords: []string{"supplier", "模型"},
			Run: func(s *launcherSession, arg string) error {
				s.app.openSection("ai")
				return nil
			},
		},
		{
			ID: cmdSettings, Names: []string{"设置", "settings", "preferences"},
			Title: "打开设置", Desc: "快捷键、开机自启与应用行为", Icon: "settings",
			Keywords: []string{"option", "偏好"},
			Run: func(s *launcherSession, arg string) error {
				s.app.openSection("settings")
				return nil
			},
		},
		{
			ID: cmdUpdate, Names: []string{"更新", "update", "检查更新"},
			Title: "检查更新", Desc: "从 GitHub Release 检查并安装新版本", Icon: "shield",
			Keywords: []string{"upgrade", "升级", "version"},
			Run: func(s *launcherSession, arg string) error {
				s.app.checkUpdate()
				return nil
			},
		},
		{
			ID: cmdQuit, Names: []string{"退出", "quit", "exit"},
			Title: "退出 Toolbox", Desc: "关闭应用，截图与翻译快捷键随之停止", Icon: "close",
			Keywords: []string{"关闭", "bye"},
			Run: func(s *launcherSession, arg string) error {
				mygo.App.Quit()
				return nil
			},
		},
	}
}

// enabled 是纯函数，渲染与执行共用；功能关闭时旧结果也不能再被回车执行。
func (c builtinCommand) enabled(a *app) bool {
	return true
}

// lookupBuiltinCommand 按 id 取命令，找不到时第二个返回值为 false。
func lookupBuiltinCommand(id builtinCommandID) (builtinCommand, bool) {
	for _, cmd := range builtinCommandTable() {
		if cmd.ID == id {
			return cmd, true
		}
	}
	return builtinCommand{}, false
}

// matchBuiltinCommand 把输入拆成「命令词 + 参数」并返回命中的命令。
// 触发词只认前缀（至少两个字符），与 VS Code / Fork / 网页搜索的触发词规则一致：
// 输入 co、fo 会给出提示行，输入 ti 就应该是图片压缩，而不是被 settings 里的
// "tin" 子串抢走。
func matchBuiltinCommand(query string) (builtinCommand, string, bool) {
	q := strings.TrimSpace(query)
	// 至少两个字符才提示，单个字母会把命令行顶到应用和文件结果前面。
	if utf8.RuneCountInString(q) < 2 {
		return builtinCommand{}, "", false
	}
	head, arg := q, ""
	if i := strings.IndexFunc(q, unicode.IsSpace); i >= 0 {
		head = q[:i]
		arg = strings.TrimSpace(q[i:])
	}
	lowerHead := strings.ToLower(head)
	if lowerHead == "" {
		return builtinCommand{}, "", false
	}

	var best builtinCommand
	bestExact, bestLen := false, 0
	for _, cmd := range builtinCommandTable() {
		for _, word := range cmd.matchWords() {
			lower := strings.ToLower(word)
			if !strings.HasPrefix(lower, lowerHead) {
				continue
			}
			exact := lower == lowerHead
			length := utf8.RuneCountInString(lower)
			// 完全相等优先于前缀；同一档里命中更长的词优先，
			// 避免 "ai" 抢走 "AI供应商" 这类长别名。
			if (exact && !bestExact) || (exact == bestExact && length > bestLen) {
				best, bestExact, bestLen = cmd, exact, length
			}
		}
	}
	if bestLen == 0 {
		return builtinCommand{}, "", false
	}
	return best, arg, true
}

// matchWords 返回命令的全部可匹配词：规范名与别名在前，关键词在后。
func (c builtinCommand) matchWords() []string {
	words := make([]string, 0, len(c.Names)+len(c.Keywords))
	words = append(words, c.Names...)
	words = append(words, c.Keywords...)
	return words
}

// isBuiltinCommand 判断一段输入是否与内置命令重名。网页搜索和项目触发词
// 用严格相等来校验，避免把 "截图一下" 这样的输入也判成冲突。
func isBuiltinCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	for _, cmd := range builtinCommandTable() {
		for _, name := range cmd.Names {
			if strings.EqualFold(name, command) {
				return true
			}
		}
	}
	return false
}
