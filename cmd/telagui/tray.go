package main

import (
	_ "embed"

	"github.com/energye/systray"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/appicon.png
var trayIconPNG []byte

// setupTray creates the system tray icon with a context menu.
func (a *App) setupTray() {
	go systray.Run(func() {
		systray.SetIcon(trayIconPNG)
		systray.SetTitle("TelaGUI")
		systray.SetTooltip("TelaGUI - Tela connection manager")

		mShow := systray.AddMenuItem("Show TelaGUI", "Show the main window")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit TelaGUI")

		// Click tray icon to show window
		systray.SetOnClick(func(menu systray.IMenu) {
			wailsRuntime.WindowShow(a.ctx)
		})

		mShow.Click(func() {
			wailsRuntime.WindowShow(a.ctx)
		})

		mQuit.Click(func() {
			a.QuitApp()
			systray.Quit()
		})
	}, func() {
		// cleanup on systray exit
	})
}
