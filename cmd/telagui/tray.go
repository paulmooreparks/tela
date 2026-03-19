package main

import (
	_ "embed"
	"runtime"

	"github.com/energye/systray"
)

//go:embed build/windows/icon.ico
var trayIconICO []byte

//go:embed build/appicon.png
var trayIconPNG []byte

// setupTray creates the system tray icon with a context menu.
func (a *App) setupTray() {
	go systray.Run(func() {
		if runtime.GOOS == "windows" {
			systray.SetIcon(trayIconICO)
		} else {
			systray.SetIcon(trayIconPNG)
		}
		systray.SetTitle("TelaVisor")
		systray.SetTooltip("TelaVisor - Tela connection manager")

		mShow := systray.AddMenuItem("Show TelaVisor", "Show the main window")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit TelaVisor")

		// Left-click tray icon to show window
		systray.SetOnClick(func(menu systray.IMenu) {
			a.ShowWindow()
		})
		// Double-click also shows window
		systray.SetOnDClick(func(menu systray.IMenu) {
			a.ShowWindow()
		})

		// Right-click shows the context menu
		systray.SetOnRClick(func(menu systray.IMenu) {
			menu.ShowMenu()
		})

		mShow.Click(func() {
			a.ShowWindow()
		})

		mQuit.Click(func() {
			// Show window first so the confirmation dialog is visible,
			// then go through the normal quit path (OnBeforeClose).
			a.ShowWindow()
			a.QuitApp()
		})
	}, func() {
		// cleanup on systray exit
	})
}
