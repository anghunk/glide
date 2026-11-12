package main

import (
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

// main 初始化原生窗口和托盘；操作系统集成始终由 MyGo 主线程驱动。
func main() {
	mygo.App.SetName(appMetadata.Name)
	mygo.App.SetVersion(appMetadata.Version)
	if runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	app, err := newService()
	if err != nil {
		log.Fatal(err)
	}
	configureUpdater()
	mygo.App.WhenReady(app.createWindows)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if app.mainWindow != nil && !hasVisibleWindows {
			app.ShowMain()
		}
	})
	mygo.App.OnSecondInstance(func([]string, string) {
		if app.mainWindow != nil {
			app.ShowMain()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { app.shutdown() })
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
