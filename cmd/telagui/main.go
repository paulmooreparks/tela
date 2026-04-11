package main

import (
	"context"
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "TelaVisor",
		Width:  1024,
		Height: 700,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose: func(ctx context.Context) bool {
			// Always save window geometry while the window is still alive.
			app.saveWindowGeometry()

			if app.IsQuitting() {
				return false // already confirmed, allow close
			}
			// TDL rule: modals capture window chrome. While any modal is
			// open the OS title-bar close must not dismiss the app; it
			// must route through the active modal's cancel flow first.
			// Ring the window (let the user know) and return true to
			// prevent close.
			if app.IsModalOpen() {
				wailsRuntime.EventsEmit(app.ctx, "app:close-blocked-by-modal")
				return true
			}
			s := app.GetSettings()
			if s.MinimizeOnClose {
				wailsRuntime.WindowHide(app.ctx)
				return true // hide to tray instead of closing
			}
			if app.IsConnected() && s.ConfirmDisconnect && !app.IsAttached() {
				// Ask JS to show the disconnect overlay
				wailsRuntime.EventsEmit(app.ctx, "app:confirm-quit")
				return true // prevent close, JS will call QuitApp if confirmed
			}
			app.confirmQuit()
			return false // allow close
		},
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		log.Printf("[telavisor] fatal: %v", err)
		os.Exit(1)
	}
}
