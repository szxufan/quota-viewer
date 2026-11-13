package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// Version 由发布构建通过 -ldflags "-X main.Version=x.y.z" 注入；开发构建为 "dev"。
var Version = "dev"

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Quota Viewer",
		Width:     60,
		Height:    60,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:  options.NewRGBA(0, 0, 0, 0),
		AlwaysOnTop:       true,
		HideWindowOnClose: true,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "quota-viewer-single-instance",
			// 二次启动时唤起已有实例的悬浮球后,新进程自动退出
			OnSecondInstanceLaunch: app.OnSecondInstanceLaunch,
		},
		DisableResize:     true,
		WindowStartState:  options.Normal,
		OnStartup:         app.OnStartup,
		OnShutdown:        app.OnShutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
