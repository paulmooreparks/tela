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
		Title:       "TelaVisor",
		Width:       1024,
		Height:      700,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose: func(ctx context.Context) bool {
			if app.IsQuitting() {
				return false // already confirmed, allow close
			}
			s := app.GetSettings()
			if s.MinimizeOnClose {
				wailsRuntime.WindowHide(app.ctx)
				return true // hide to tray instead of closing
			}
			if app.IsConnected() && s.ConfirmDisconnect {
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
