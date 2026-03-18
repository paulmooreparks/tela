package main

import (
	"context"
	"embed"

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

	// Read settings before window creation for StartHidden
	settings := app.GetSettings()

	err := wails.Run(&options.App{
		Title:       "TelaGUI",
		Width:       1024,
		Height:      700,
		StartHidden: settings.StartMinimized,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose: func(ctx context.Context) bool {
			if app.IsQuitting() {
				return false // allow quit
			}
			s := app.GetSettings()
			if s.MinimizeToTray {
				wailsRuntime.WindowHide(app.ctx)
				return true // prevent close, hide instead
			}
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
		println("Error:", err.Error())
	}
}
