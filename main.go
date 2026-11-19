package main

import (
	"log"
	"runtime"

	"github.com/egoist/mygo"
	// 应用文案是中文，但 mygo 自带的文案（托盘“退出”等）需要显式引入
	// 语言包才会显示中文；项目没有语言目录，所以在这里导入。
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/plugins/updater"
	updaternative "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

func main() {
	a := newApp()
	if !mygo.App.RequestSingleInstanceLock() {
		return // 已有实例在运行，第二实例会触发 OnSecondInstance 唤起主窗口。
	}
	// 应用文案本身就是中文，但项目没有 i18n 语言目录，mygo 默认会把应用当作
	// “只写了英文”，于是更新窗口、托盘里的“检查更新”和各状态提示全回落成英文。
	// 显式声明中文，更新插件自带的 zh-Hans 翻译才会生效。
	mygo.App.SetLanguages("zh-Hans")
	// 更新窗口用原生 UI 版本（本项目没有 webview）。
	// 没有 updates 配置的开发构建里插件不会真正检查，界面会给出提示。
	mygo.Use(updaternative.Plugin)
	a.updatesOn = true
	mygo.App.SetName(appName)
	// 日志必须在 Run 之前接管：打包后的 Windows 版本没有 stderr。
	setupLogging()
	// 关窗不退出：应用常驻托盘。
	mygo.App.OnWindowAllClosed(func() {})
	// 退出前先记下来：窗口的 OnClose 里要区分“用户关窗（隐藏）”和“应用退出（真关）”，
	// 否则 PreventDefault 会拦掉退出流程，导致点退出没反应。
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.quitting = true
		if a.compressor != nil {
			a.compressor.cancel()
		}
	})
	mygo.App.OnSecondInstance(func([]string, string) { a.showLauncher() })
	mygo.App.WhenReady(a.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// ready 在应用就绪后装配窗口、托盘与全局快捷键。
func (a *app) ready() {
	if bmp, err := ui.DecodeBitmap(iconPNG); err == nil {
		a.icon = bmp
	}
	a.load()

	// 开机自启时由系统在登录后拉起进程，用户此时并没有要打开应用：
	// 只保留托盘常驻，需要时再从托盘或快捷键进入。
	// 首次安装的第一次启动仍然弹出功能中心（里面是欢迎卡），好让用户知道
	// 主入口换成了启动器。必须在这里用 Hidden 声明：原生 UI 窗口创建后
	// mygo 会自己排一次 settleShow，只跳过后面的 Show 挡不住它。
	openedAtLogin := mygo.App.WasOpenedAtLogin()
	showMain := shouldShowMainOnLaunch(openedAtLogin, a.settings)

	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     appName,
		Width:     1080,
		Height:    760,
		MinWidth:  900,
		MinHeight: 640,
		// 记住上次的窗口位置与大小。
		StateKey: "main",
		Hidden:   !showMain,
		Content:  ui.View(a.view),
	})
	// 关闭即隐藏，保留界面状态；应用真正退出时放行。
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		a.hideMain()
	})

	a.setUpTray()
	a.registerHotkeys()
	a.watchUpdates()
	// 配置里的开机自启只是本地缓存：用户可能在系统设置或任务管理器里
	// 把登录项删掉，那里才是唯一真相，启动时对一次账免得开关状态说谎。
	if actual := mygo.App.OpenAtLogin(); actual != a.settings.LaunchAtLogin {
		a.settings.LaunchAtLogin = actual
		_ = a.persist()
	}
	// 启动器启用且自定义命令开启时，提前探测 code 命令；
	// 结果会在后台回来后刷新设置页，不阻塞启动。
	if a.settings.LauncherVSCodeEnabled {
		a.detectVSCodeCommand()
	}
	if a.settings.LauncherForkEnabled {
		a.detectForkCommand()
	}
	if a.settings.LauncherTraeEnabled {
		a.detectTraeCommand()
	}
	if openedAtLogin {
		// macOS 还要收回 Dock 图标，与“关闭主窗口”后的状态保持一致，
		// 否则 Dock 上会多出一个点了没反应的图标。
		mygo.App.Dock.Hide()
		return
	}
	if !showMain {
		// 用户已经走完引导：冷启动只驻留托盘，用快捷键唤起启动器。
		mygo.App.Dock.Hide()
		return
	}
	a.win.Show()
}

// shouldShowMainOnLaunch 决定冷启动时是否弹出功能中心。登录拉起一律不弹；
// 其余情况只在首次引导没走完时弹一次。
func shouldShowMainOnLaunch(openedAtLogin bool, s Settings) bool {
	if openedAtLogin {
		return false
	}
	return !s.HasCompletedOnboarding
}

// watchUpdates 把插件检查到的新版本同步到界面（检查在后台进行，
// 主线程重绘要靠这里推一下）。
func (a *app) watchUpdates() {
	if !a.updatesOn {
		return
	}
	updater.OnChange(a.refreshUpdateState)
	a.refreshUpdateState()
}

// showMain 打开主窗口，同时恢复 Dock 图标：本应用常驻菜单栏，
// Dock 只在主窗口开着时出现，避免留下一个点了没反应的图标。
func (a *app) showMain() {
	if a.win == nil {
		return
	}
	mygo.App.Dock.Show()
	a.win.Show()
	a.win.Focus()
}

// hideMain 收起主窗口，并把 Dock 图标一并收回（转为菜单栏应用）。
// 打开过功能中心就标记引导完成，下次冷启动不再自动弹窗。
func (a *app) hideMain() {
	if a.win == nil {
		return
	}
	a.win.Hide()
	mygo.App.Dock.Hide()
	if !a.settings.HasCompletedOnboarding {
		a.settings.HasCompletedOnboarding = true
		a.persist()
	}
}

func (a *app) setUpTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        appName,
	})
	if err != nil {
		log.Print("创建托盘失败：", err)
		return
	}
	a.tray = tray

	items := []*mygo.MenuItem{
		{ID: "tray-launcher", Label: "打开启动器", Accelerator: a.settings.Hotkeys.Launcher,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.startLauncher() }},
		{Label: "功能中心", Click: func(*mygo.MenuItem, *mygo.Window) { a.showMain() }},
		mygo.Separator(),
		// Accelerator 由系统菜单绘制在右侧，macOS 自动显示 ⌥、⌘ 等符号。
		// 截图和翻译保留独立入口，快捷键也照旧可录制，启动器只是多一层入口。
		{ID: "tray-translate", Label: "划词翻译", Accelerator: a.settings.Hotkeys.Translate,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.startTranslation() }},
		{ID: "tray-capture", Label: "区域截图", Accelerator: a.settings.Hotkeys.Screenshot,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.startCapture() }},
		mygo.Separator(),
	}
	if a.updatesOn {
		items = append(items, updater.MenuItem(), mygo.Separator())
	}
	items = append(items, &mygo.MenuItem{Role: mygo.RoleQuit})
	menu := mygo.NewMenu(items)
	a.trayItems = items

	if runtime.GOOS == "linux" {
		// AppIndicator 只支持菜单。
		tray.SetMenu(menu)
		return
	}
	// 左键打开功能中心，右键弹出菜单（避免主点击被菜单吞掉）。
	tray.OnClick(a.showMain)
	tray.OnRightClick(func() {
		a.refreshTrayShortcuts()
		tray.SetMenu(menu)
		defer tray.SetMenu(nil)
		tray.PopUpMenu()
	})
}

// refreshTrayShortcuts 把最新快捷键同步到托盘菜单，录制新组合键后立即生效。
func (a *app) refreshTrayShortcuts() {
	for _, it := range a.trayItems {
		var acc string
		switch it.ID {
		case "tray-translate":
			acc = a.settings.Hotkeys.Translate
		case "tray-capture":
			acc = a.settings.Hotkeys.Screenshot
		case "tray-launcher":
			acc = a.settings.Hotkeys.Launcher
		default:
			continue
		}
		if it.Accelerator != acc {
			it.SetAccelerator(acc)
		}
	}
}
