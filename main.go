package main

import (
	"log"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	a := newApp()
	if !mygo.App.RequestSingleInstanceLock() {
		return // 已有实例在运行，第二实例会触发 OnSecondInstance 唤起主窗口。
	}
	mygo.App.SetName(appName)
	// 关窗不退出：应用常驻托盘。
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnSecondInstance(func([]string, string) { a.showMain() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.destroyTray() })
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

	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     appName,
		Width:     1080,
		Height:    760,
		MinWidth:  900,
		MinHeight: 640,
		// 记住上次的窗口位置与大小。
		StateKey: "main",
		Content:  ui.View(a.view),
	})
	// 关闭即隐藏，保留界面状态。
	a.win.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		a.win.Hide()
	})

	a.setUpTray()
	a.registerHotkeys()
	a.win.Show()
}

func (a *app) showMain() {
	if a.win == nil {
		return
	}
	a.win.Show()
	a.win.Focus()
}

func (a *app) toggleMain() {
	if a.win == nil {
		return
	}
	if a.win.IsVisible() && a.win.IsFocused() {
		a.win.Hide()
	} else {
		a.showMain()
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

	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开 Toolbox", Click: func(*mygo.MenuItem, *mygo.Window) { a.showMain() }},
		mygo.Separator(),
		{Label: "划词翻译", Click: func(*mygo.MenuItem, *mygo.Window) { a.startTranslation() }},
		{Label: "区域截图", Click: func(*mygo.MenuItem, *mygo.Window) { a.startCapture() }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})

	if runtime.GOOS == "linux" {
		// AppIndicator 只支持菜单。
		tray.SetMenu(menu)
		return
	}
	// 左键切换主窗口，右键弹出菜单（避免主点击被菜单吞掉）。
	tray.OnClick(a.toggleMain)
	tray.OnRightClick(func() {
		tray.SetMenu(menu)
		defer tray.SetMenu(nil)
		tray.PopUpMenu()
	})
}

func (a *app) destroyTray() {
	if a.tray != nil {
		a.tray.Destroy()
		a.tray = nil
	}
}
