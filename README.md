# Toolbox

轻巧的桌面工具箱，基于 Go + [MyGo](https://github.com/egoist/mygo) 构建，支持 macOS 与 Windows。

## 功能

- **截图标注**：按快捷键冻结当前屏幕，框选后可矩形、椭圆、直线、箭头、画笔、文字、马赛克标注，支持撤销/重做，复制到剪贴板、保存为 PNG 或贴图。贴图会把选区钉在桌面，可随意拖动，适合把多张截图并排比较；右键或 `Esc` 关闭。多显示器下截取光标所在的屏幕。
- **划词翻译**：在任意应用中选中文本，按快捷键读取选区并弹出翻译浮窗。可同时对照百度翻译、Google 翻译和任意 OpenAI 兼容服务的结果，支持目标语言切换、手动编辑原文与逐卡片复制。
- **快速启动**：按快捷键唤起搜索浮窗，按名称筛选已安装应用和常用目录中的文件。应用结果显示真实图标；`↑` / `↓` 切换选中项，回车打开，`Esc` 或点击窗口外部关闭。搜索源可在「启动」页分别开关。
- **VSCode 快速打开**：快速启动的自定义命令，默认关闭。开启后可配置命令（默认 `code`）和多个项目文件夹；输入 `code `（命令 + 空格）后，列出这些文件夹的下一级项目，回车用 VSCode 打开。未检测到命令时会在用户目录自动配置一个仅供启动器使用的快捷命令，不修改系统 `PATH`。

## 下载

从 [Releases](https://github.com/ShakePeng/toolbox/releases) 下载：

| 平台 | 文件 |
| --- | --- |
| Windows 10/11 x64 | `toolbox-setup-<版本>-amd64.exe`（安装包）或 `toolbox-<版本>-windows-amd64.tar.gz` |
| macOS（Apple Silicon） | `toolbox-<版本>-arm64.dmg` |

Windows 安装包未做代码签名，首次运行可能出现 SmartScreen 提示，选择「更多信息 → 仍要运行」即可。macOS 版本使用自签名证书，首次打开需要在「系统设置 → 隐私与安全性」中选择「仍要打开」。

应用内支持从 GitHub Release 自动更新，更新包会使用内置公钥校验签名。

## 默认快捷键

| 功能 | Windows | macOS |
| --- | --- | --- |
| 划词翻译 | `Ctrl+Alt+D` | `Alt+D` |
| 截图标注 | `Ctrl+Alt+S` | `F1` |
| 快速启动 | 未设置 | 未设置 |

快捷键可以在应用内的「翻译」「截图」「启动」页重新录制。快速启动默认不占用组合键，需要先在概览中开启插件，再到「启动」页录制。已有配置升级后保持不变；Windows 上旧版本误录成的 `Cmd+…` 会在升级时自动清理。

## 权限与平台限制

- macOS：截图需要「屏幕录制」权限，划词翻译需要「辅助功能」权限；首次使用按应用内提示授权。快速启动索引桌面、文档、下载时，如果系统询问文件访问权限，需要允许后才能搜到对应目录。
- Windows：截图使用 GDI，无需系统授权。划词翻译通过模拟 `Ctrl+C` 读取选区，**在终端类应用（Windows Terminal、VS Code 终端等）中不可用**，因为 `Ctrl+C` 会被终端当作中断信号；以管理员权限运行的应用也可能无法读取。
- Windows 划词的读取过程会临时替换剪贴板，读完恢复原内容。

## 开发

需要 Go 1.27.3 或更高版本。

```bash
go tool mygo dev          # 开发模式，修改 Go 代码后自动重建
go test ./...             # 单元测试
go tool mygo vet .        # 检查原生 UI 的构建生命周期问题
```

macOS 本地开发如果想复用固定签名身份（避免每次重建后重新授权），先执行 `bash scripts/macos-signing.sh`，再用 `bash scripts/dev.sh` 启动。

## 发布

版本号由 `mygo.json` 的 `version` 决定，需要与 `CHANGELOG.md` 中对应小节一起更新：

```bash
scripts/release.sh        # 检查版本、工作区和测试
scripts/release.sh --push # 打 tag 并推送，触发 GitHub Actions 构建发布
```

## 目录

| 路径 | 说明 |
| --- | --- |
| `main.go` | 应用入口、窗口/托盘/快捷键装配 |
| `app.go` | 主界面与交互逻辑 |
| `capture*.go` | 截图覆盖层与各平台屏幕捕获 |
| `selection_*.go` | 各平台读取选中文本 |
| `translate.go` | 翻译服务请求与语言判断 |
| `translation_window.go` | 划词翻译浮窗 |
| `launcher*.go` | 快速启动浮窗、搜索匹配、文件索引与应用图标 |
| `config.go` / `model.go` | 本地配置读写与数据模型 |
| `signing/` | macOS 固定签名材料（说明见 `signing/README.md`） |
