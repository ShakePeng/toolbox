# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概况

Toolbox 是一个用 Go + [MyGo](https://github.com/egoist/mygo) 构建的原生桌面工具箱（无 webview、无前端框架），发布 macOS（Apple Silicon）与 Windows 两个平台。当前有三个插件：

- **截图标注**：冻结整屏 → 框选 → 矩形/椭圆/直线/箭头/画笔/文字/马赛克标注 → 复制、保存 PNG 或贴图。
- **划词翻译**：在任意应用选中文本，按快捷键读取选区，并发请求百度、Google 和任意 OpenAI 兼容服务，在浮窗里并排对照。
- **快速启动**：按快捷键唤起搜索浮窗，按名称筛选已安装应用和常用目录中的文件，上下键切换、回车打开。

整个仓库是单个 `main` 包，没有 `internal/` 或子模块。界面文案、代码注释、提交信息统一用中文。

## 技术栈与依赖

| 项 | 说明 |
| --- | --- |
| Go | `go.mod` 要求 1.27.3，MyGo 以 `tool` 指令固定（`go tool mygo`） |
| UI | `github.com/egoist/mygo/ui`，即时构建式原生 UI |
| 系统调用 | `github.com/ebitengine/purego`（macOS CoreGraphics / Accessibility / AppKit、Windows GDI） |
| 更新 | `mygo/plugins/updater` + `updater/native`，从 GitHub Release 下载并校验签名 |
| 权限 | `mygo/plugins/permissionflow`（辅助功能、屏幕录制） |
| 网络 | `mygo/netproxy`（按服务开启系统代理） |

仓库内的 MyGo 维护指南 `.agents/skills/mygo-maintenance/SKILL.md` 是改 UI 前必读的规则来源；升级 MyGo 前先用 `go list -m -f '{{.Dir}}' github.com/egoist/mygo` 找到源码核对 API。

## 常用命令

```bash
# 开发
go tool mygo dev                       # 开发模式，改 Go 代码后自动重建
bash scripts/dev.sh                    # macOS：用固定签名身份跑 dev（先执行 scripts/macos-signing.sh）

# 测试
go test ./...                          # 全部测试
go test -run TestPinBounds -v .        # 单个测试（根目录就是唯一包）
go test -race ./...                    # 改共享状态时
CGO_ENABLED=0 go test ./...            # 与 CI 的 test job 一致
RENDER=1 go test -run TestRenderSnapshots -v   # 渲染页面快照到 /tmp/toolbox_render

# 静态检查
go tool mygo vet .                     # MyGo 生命周期/goroutine 捕获检查（CI 用）
go vet ./...                           # release.sh 用
GOOS=windows go build ./...            # 本机验证 Windows 平台文件能编译（不能运行）

# 构建与发布
go tool mygo build -platform "darwin/arm64,windows/amd64" -sign "Toolbox Local Dev" -upload
scripts/release.sh                     # 发版前检查（dry-run）
scripts/release.sh --push              # 打 tag 并推送，触发 GitHub Actions
```

没有独立 lint 配置；`gofmt` + `go vet` 就是格式/静态检查基线。Windows 专属代码（`capture_windows.go`、`selection_windows.go`）带构建标签，本地只能在 Windows 上跑测试。

## 架构

### 单包结构与启动流程

入口 `main.go`：

1. `newApp()` 创建全局状态，取得单实例锁；第二实例由 `OnSecondInstance` 唤起已有窗口。
2. `SetLanguages("zh-Hans")`：项目没有 i18n 语言目录，不显式声明时 MyGo 自带的托盘/更新文案会回落英文。
3. `mygo.Use(updaternative.Plugin)` 装配更新插件；`a.updatesOn = true` 是后续更新逻辑的开关。
4. `setupLogging()` 必须在 `Run()` 之前：打包后的 Windows GUI 进程没有 stderr，日志要落盘到 `PathLogs`（`logging.go`，超过 2MB 时轮转成 `.log.1`）。
5. 主窗口关闭只隐藏（`OnClose` 里 `PreventDefault`）；`OnBeforeQuit` 设置 `quitting`，退出时才真正放行关闭。
6. `ready()` 里加载配置、创建主窗口（`StateKey: "main"` 记住位置尺寸）、托盘、全局快捷键。

托盘：左键切换主窗口显示/隐藏，右键弹菜单（划词翻译、区域截图、快速启动、检查更新、退出）；Linux 的 AppIndicator 只支持菜单，因此只 `SetMenu`。托盘菜单项的快捷键由 `refreshTrayShortcuts` 在每次弹出前同步。

### 窗口模型

应用里有五种窗口，都是 MyGo 原生窗口：

| 窗口 | 特征 | 生命周期 |
| --- | --- | --- |
| 主窗口 | 普通窗口，关闭即隐藏 | 全局唯一，常驻 |
| 截图覆盖层 | 无边框、全屏、置顶、跨工作区、不可移动/缩放 | 每次截图一个，结束 `Destroy` |
| 贴图窗口 | 无边框、置顶、跨工作区、可拖动 | 每张贴图一个，关闭后从 `a.pins` 移除 |
| 翻译浮窗 | 无边框、透明、置顶、失焦自动隐藏 | 全局复用一个 `translationSession` |
| 启动器浮窗 | 无边框、透明、置顶、失焦自动隐藏、屏幕上部居中 | 全局复用一个 `launcherSession`，关闭插件时 `dispose` |

`app` 持有 `capture`、`pins`、`trans`、`launcher` 四个引用；退出/关闭路径要保证引用被清理，否则下次无法重新打开。

### UI 构建规则（最关键）

`app.view` 是根视图，所有界面由 `ui` 的即时构建 API 组成。必须遵守 MyGo 的生命周期约束：

- **不要把 `ui.Element` 或 parts 存进 app 状态**。它们在下次 build pass（甚至同一帧的下一次 pass）后失效。需要持久身份时用 `ui.Handle` + `.Bind(&handle)`；有状态控件用 `c.Key(key)` 声明稳定 key。
- 一次事件可能触发多次重建，**副作用只能写在 `OnClick` / `Changed` / `Clicked()` 等已处理回调里**，不能写在无条件执行的渲染路径上（否则重复保存、重复请求）。
- 慢 I/O 放 goroutine，结果通过 `win.Update(func(){...})` 回主线程。异步结果要用序号或状态判断丢弃过期数据（翻译浮窗的 `reqID` 是范例）。
- 控件几何在输入回调里通过 `handle.Bounds(c)` 读，不要捕获元素再查询。
- `ui.Cmd` 在 macOS 是 Command，在 Windows/Linux 等于 Ctrl；`hotkey.go` 里专门避免非 macOS 输出 `Cmd`，否则会被解析成 Win 键导致注册失败。

主界面结构：侧栏（品牌 + 可见导航）+ 内容区。`section` 取 `overview | translation | screenshot | launcher | settings`；`visibleSections` 根据三个插件开关决定是否显示对应 Tab，关闭插件后若当前 section 不可见会回退概览。响应式规则：窗口宽 `<980` 视为紧凑（侧栏 196、内边距 24），内容宽度封顶 1120；概览插件卡在 840/560 阈值下降为 3/2/1 列，Provider 卡片在 660/430 阈值下降为 3/2/1 列。主题固定深色暖琥珀（`theme.go`），不跟随系统明暗。

### 状态与配置

- 配置路径是 `mygo.App.Path(PathUserData)/config.json`，模型在 `model.go`（camelCase，对齐原 Tauri 项目的配置结构），读写与迁移在 `config.go`。
- `loadConfig`：文件不存在时写默认配置；解析失败回退默认值并记录 `loadErr`，不阻塞启动。
- `migrateConfig` 负责：补 `schemaVersion`、补默认快捷键、非 macOS 去掉旧版误录的 `Cmd`、补 `TargetLang`、补 OpenAI 默认 Base URL、重算 `Configured`。迁移必须逐级判断：v1→v2 给启动器搜索源补 `true`，v2→v3 给 VSCode 命令补 `code`；不能统一用 `< configSchemaVersion`，否则 v2 用户显式关闭的搜索源会被覆盖。`persist()` 必须写 `configSchemaVersion`。
- 写入是原子的：先写 `.tmp` 再 `rename`，权限 `0600`。
- **设置页没有保存按钮**，快捷键、目标语言、开关等改动立刻 `persist()`；测试 `TestManualSaveButtonsRemoved` 会守住这个约定。
- **翻译凭据目前明文存在 config.json**（原版用系统钥匙串），代码注释已标为后续迁移项。新增字段时不要假设已有加密。

### 截图流水线

1. `startCapture`（`capture.go`）用光标位置找当前显示器；抓取区域是 `disp.Bounds`（整屏），不是 `WorkArea`（后者会裁掉菜单栏/程序坞）。
2. macOS 先通过 `CGPreflightScreenCaptureAccess` 检查权限（`perm_screen_darwin.go`），没权限就申请并提示；避免无权限时 `screencapture` 静默只抓壁纸。
3. 先收起翻译浮窗和主窗口，再 `captureRegion`：macOS 调 `/usr/sbin/screencapture -x -t png -R`；Windows 用 GDI `BitBlt` + `GetDIBits`（进程是 Per-Monitor V2 DPI 感知，拿到的是物理像素）；其他平台直接返回错误。
4. 创建全屏覆盖层：`show` 后用 `mygo.RunOnMain` 调 `coverSystemUI`（macOS 去掉 titled 样式、层级提到 1000）、重新 `SetBounds` 整屏、`focusWindowContent` 让键盘事件可用。`safeBottom` 取自 `WorkArea`，保证工具栏不会落到程序坞下面点不到。
5. 覆盖层渲染冻结画面（`bitmap`）+ 整体压暗 + 选区内还原；标注统一裁剪到选区，绘制工具在选区外无效。`marks.go` 只放纯几何/绘制逻辑（命中检测、矩形归一化、马赛克采样），便于单测。
6. 输入：`Esc`/右键退出；`Enter`/`Cmd+C` 复制；`Cmd+S` 保存；`Delete`/`Backspace` 删除选中标注；`Cmd+Z` / `Shift+Z` 撤销重做；双击选区内复制、选区外双击全屏；点击别处会让正在编辑的文字落笔。
7. 输出通过 `requestComplete` 延后一帧：先 `flushText` 让文字框收起，下一帧再 `win.CapturePage()`，否则输入框会被截进图里。
8. `complete()` 把覆盖层像素按 `物理像素 / 逻辑尺寸` 换算后裁剪（四周内缩 1 点去边框），再复制到剪贴板 / 弹保存面板（`Parent` 绑定当前覆盖层，避免多显示器弹到另一块屏）/ 创建贴图。保存或贴图失败时保留覆盖层可重试。
9. 覆盖层是全屏置顶窗口，绘制/输入/初始化都用 `recoverAndClose` 兜底 panic：异常时关闭蒙层并写日志，绝不把用户锁在窗口里。

### 贴图（`pin.go`）

`pinBounds` 把覆盖层选区换算成屏幕坐标（同样内缩 1 点）。贴图窗口 `ShowInactive`，不抢当前应用焦点；`OnReadyToShow` 里先定位再显示，避免闪白。窗口整体 `DragWindow` 可拖动，悬停显示关闭按钮，右键或 `Esc` 关闭，跨工作区可见。

### 划词翻译

**读取选区**（`selection_*.go`）：

- macOS 优先走 Accessibility：`readSelectedTextAX` 依次尝试系统级焦点元素、焦点应用、按前台 PID 创建应用元素；对 Electron/Chromium 会显式打开 `AXManualAccessibility` / `AXEnhancedUserInterface` 并等待辅助功能树构建。
- 只有 `errNoFocusedElement` 不兜底；空选区、元素失效、`AXSelectedText` 未暴露等情况退回剪贴板方式（模拟 `Cmd+C`，读完恢复原剪贴板）。
- Windows 没有统一选区接口，直接模拟 `Ctrl+C`：先等修饰键抬起，备份剪贴板，写唯一标记确认复制真的发生，失败重试一次，最后恢复原剪贴板。终端类应用不可用是已知限制。
- Purego 调 C 的注意点：传给 C 的指针要用 `runtime.KeepAlive` 保证存活；`defer cfRelease(focused)` 若在闭包外会在 defer 求值时固定旧指针，重取焦点元素后必须用闭包形式避免 double free。

**请求**（`translate.go`）：

- `translationProviders` 只保留 `Enabled && Configured` 的服务；OpenAI 兼容服务还必须有 Base URL。
- `translateAll` 并发请求，结果顺序与配置一致，单个失败不影响其他服务，超时 15s。
- 接口地址（`baiduEndpoint`、`googleEndpoint`）是包级变量，测试用 `httptest` 替换。
- `newHTTPClient` 默认 `Proxy = nil`，仅当服务开启「走系统代理」时接入 `netproxy.Proxy`。
- 百度用 MD5 签名；Google 用 Translation Basic 的 API Key；OpenAI 走 Chat Completions，目标语言用英文名写进提示词。

**浮窗**（`translation_window.go`）：

- 单个会话复用，首次划词创建。`startRequests` 递增 `reqID`，旧请求返回时直接丢弃。
- `fitHeight` 手工估算窗口高度（原生 UI 没有内容自适应），内容超出时中间区域 `Scroll` 兜底；高度限制在 240–560。
- 目标语言下拉切换后会重新请求；`Esc` 先关下拉再关窗口；失焦自动隐藏（退出过程中不拦）。
- 提示（权限缺失、无选区、复制成功等）复用浮窗展示，因为此时主窗口可能是隐藏的。

**语言判断**：目标语言为 `auto` 时，原文含汉字且不含假名/谚文 → 英语，否则 → 简体中文。不能只判断汉字，否则日文会被误判成中文。

### 快速启动（`launcher*.go`）

- 全局快捷键默认**空**，`ensureHotkey` 对空值直接跳过注册；插件默认关闭，开启后提示用户到「启动」页录制。
- 复用一个置顶浮窗（宽 640，高度 67–540 自适应）：空查询只显示搜索框，有结果时按行数增高，最多 8 行。`show` 重置查询、聚焦无边框的 `TextInputBase`；`OnTextKey` 处理 `↑/↓/Enter/Esc`。`ui.TextKey` 只在回调期间有效，**不能保存**。焦点离开输入框后，根视图用 `c.Shortcut(0, ui.KeyEscape)` 兜底关闭。
- 失焦自动隐藏；刚 `Show` 的 150ms 内忽略失焦；因失焦隐藏后的 250ms 内吞掉一次 toggle，避免快捷键先触发失焦、回调又把窗口弹回来。
- Windows 11 会给 `Panel` 圆角窗口描一圈 DWM 边框；UI 若再画 `Border` 就会看到两条平行边框。`hideNativeWindowBorder`（`launcher_border_windows.go`）用 `DWMWA_BORDER_COLOR = DWMWA_COLOR_NONE` 去掉原生描边，创建窗口和 `Show` 后各调用一次。翻译浮窗使用同一处理。
- 应用列表由 `Shell.InstalledApps()` 在后台 goroutine 枚举；文件索引在首次唤起时后台构建。两者都通过 `win.Update` 回主线程，并用 `gen` 丢弃重建前的旧结果。
- 文件索引（`launcher_index.go`）只扫桌面、下载、文档（深度 4）和主目录浅层（深度 1），跳过隐藏项、系统/缓存/构建目录、`.app` 等包目录和临时文件；符号链接不跟随。总计上限 5000 条、单根 2500 条。
- 真实应用图标通过 `Shell.FileIcon` 加载；它内部会切主线程，所以只能在事件路径（`prefetchIcons`）按当前结果最多 8 个、并发 3 加载。`view()` 只查缓存，绝不能在渲染路径调用 `FileIcon`。
- 搜索评分在 `launcher_search.go`：多词 AND，前缀 > 词首 > 子串；空查询只列应用。搜索源只在主窗口「启动」页切换，切换后立即 `persist()`，并通过 `sourcesChanged()` 同步已打开的浮窗。
- **VSCode 快速打开**（`vscode.go` / `launcher_projects.go`）：启动器设置页的子开关，默认关闭。配置项是命令（默认 `code`）和多个项目文件夹（原生目录多选）。
  - 输入「命令 + 空格」进入项目模式；`projectQuery` 不能先 `TrimSpace`，否则触发空格会被吃掉。只扫描每个配置文件夹的下一级子目录，不递归，跳过隐藏项和符号链接，最多 2000 条。
  - 命令探测在后台串行执行（`vscodeProbeMu`）：先 `exec.LookPath`，找不到再查平台常见安装位置；找到但不在 PATH 时，在 `~/.toolbox/bin`（Windows）或 `~/.local/bin`（Unix）创建仅供启动器使用的 shim/symlink。**不修改系统 PATH**，也不会自动安装 VSCode 本体。
  - Windows 启动 `.cmd/.bat` 必须经 `cmd.exe /c`，并用 `SysProcAttr{HideWindow:true}` 隐藏控制台（`vscode_run_windows.go`）。
  - 项目扫描用 `projectGen` 丢弃旧结果；设置变化通过 `vscodeSettingsChanged()` 清空缓存并重扫。探测失败后不要每个按键都重试，`vscode.checked` 负责防重。

### 快捷键

- 默认值按平台区分（`model.go`）：macOS `Alt+D` / `F1`；其他平台 `Ctrl+Alt+D` / `Ctrl+Alt+S`，避免占用系统常用键。快速启动的 `Launcher` 在所有平台都默认空。
- 录制（`hotkey.go`）：把物理键转成 accelerator，普通键必须带修饰键，功能键可单独使用。**录制前必须先注销所有全局快捷键**，否则按当前组合键会被全局注册截走；录制结束恢复注册并立即落盘。
- 任意两个插件使用相同组合键时给冲突提示（`findHotkeyConflict` 忽略空值）；注册失败会 toast，不再静默失效。
- 托盘菜单的 accelerator 在右键弹出前刷新，录制后无需重启。

### 平台适配与资源

- 平台差异全部用构建标签：`capture_*`、`selection_*`、`perm_screen_*`、`window_level_*`。Linux 目前没有截图和选区实现。
- macOS 覆盖层层级/样式、键盘焦点通过 Objective-C runtime 调整（`window_level_darwin.go`）。
- 图标：`resources/icon.png` 用 `go:embed` 嵌入；线性图标和品牌图标是 SVG 字符串表，初始化时 `ui.MustParseSVG` 解析一次，避免每帧重复解析。
- 应用版本唯一来源是 `mygo.json` 的 `version`；`appVersion()` 优先读打包信息，开发构建回退到内嵌的 `mygo.json`。

## 测试

- UI 测试用 `ui.NewTester(view, w, h)`，可 `HasText`、`Click`、模拟输入/快捷键并多次 `Frame()`。断言依赖系统中文字体，所以 CI 的 test job 固定在 `macos-latest`。
- `render_test.go` 只在 `RENDER=1` 时运行，把各页面（含启动页和启动器浮窗）、不同窗口宽度渲染成 PNG 到系统临时目录的 `toolbox_render`，用于人工核对响应式布局，不参与 CI。
- 翻译测试用 `httptest`，不访问真实接口；覆盖语言判断、Provider 过滤、百度错误码等。
- 启动器测试分四层：`launcher_search_test.go` 覆盖评分/过滤/排序；`launcher_index_test.go` 用临时目录验证跳过规则、深度和上限；`launcher_projects_test.go` 覆盖「命令 + 空格」解析、下一级目录扫描和项目过滤；`launcher_test.go` 用 `ui.NewTester` 渲染浮窗，并注入假应用/文件/项目和 `launchApp`/`openPath`/`launchProject`，不启动真实应用。
- `vscode_test.go` 覆盖未知命令探测、shim 不覆盖已有文件、Windows 安装路径和缺失项目目录；不会真的启动 VSCode。
- Windows 专属代码在单独的 `windows-latest` job 运行，macOS 的测试覆盖不到。
- 改动共享状态时加 `-race`；改 UI 行为时优先补 Tester 测试。

## 发版、签名与更新

- 版本流程：改 `mygo.json` 的 `version` + 补 `CHANGELOG.md` 对应小节 → `scripts/release.sh` 检查（工作区干净、tag 不存在、测试通过）→ `--push` 打 `v<version>` tag。workflow 会校验 tag 与版本一致，并用 CHANGELOG 当前小节生成 release 说明。
- CI 构建在 macOS runner 上完成 macOS + Windows 产物，Windows 安装包依赖 `makensis`；更新包需要仓库 secret `MYGO_UPDATER_PRIVATE_KEY`。
- macOS 使用仓库内固定自签名证书（`signing/`，身份 `Toolbox Local Dev`，SHA-1 记录在 `scripts/macos-signing.sh` 和 workflow 中）。目的不是“可信”，而是让 TCC 授权的 designated requirement 从「每次构建都变的 cdhash」变成「bundle id + 证书指纹」，这样升级后屏幕录制/辅助功能授权不会丢。
- `go tool mygo dev` 默认 ad-hoc 签名，开发包每次重建都会丢授权；要复用固定身份用 `scripts/dev.sh`。开发包 bundle id 带 `.dev` 后缀，与正式包是两个独立的 TCC 应用。
- 不要重新生成签名证书：换证书会让所有用户的授权失效。换证书时必须同步更新脚本和 workflow 里的 SHA-1 校验。

## 已知限制

- 翻译凭据明文保存在 `config.json`。
- Linux 没有截图和选区读取实现；Windows 划词在终端类应用不可用，管理员权限运行的应用也可能读不到。
- macOS 15 对直接抓屏有约 30 天一次的「Allow For One Month」确认，与签名无关。
- 自签名证书不能公证，新机器首次打开需在「隐私与安全性」里选择「仍要打开」；证书 2036-11-06 到期。
- 原生 UI 没有内容自适应高度，翻译浮窗高度是估算值（`fitHeight`），改布局时要同步调整估算。启动器浮窗按 `launcherHeaderHeight` / `launcherRowHeight` 等常量估算高度（67–540），改行内边距或字号时要同步调整这些常量；结果区仍用 `Scroll` 兜底。
- 快速启动的文件索引只覆盖桌面、下载、文档和主目录浅层，不做全盘搜索、全文搜索或实时监听；索引在首次唤起时建立，应用运行期间不会自动重建（可在「启动」页手动重建）。
- VSCode 快速打开只扫描每个项目根目录的下一级子目录，不递归，也不按 `.git`/`package.json` 判断是否为项目。自动配置的快捷命令位于用户目录（`~/.toolbox/bin` 或 `~/.local/bin`），仅供启动器使用，不改系统 PATH；不会自动安装 VSCode 本体。
- macOS 的桌面/文档/下载受 TCC 文件访问限制时，索引会跳过无权限目录；浮窗底部和「启动」页会显示提示。

## 开发约定

- 保持改动小而聚焦，沿用现有的即时 UI 写法和命名；新增界面优先复用 `app.go` 里的 `intro`、`iconTile`、`pill`、`primaryBtn`、`secondaryBtn`、`ghostBtn` 等部件。
- 注释解释「为什么」，重点写平台差异、权限/TCC、剪贴板副作用、坐标换算、生命周期陷阱这类从代码看不出的背景。
- `README.md` 面向用户，只放功能、下载、权限说明；不要加入本机路径或安装章节。
- 发布推送后不需要盯流水线，用户自己会看构建与 Release 结果。
