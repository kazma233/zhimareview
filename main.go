package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	// 单实例锁保证再次启动不另起进程；单窗多 tab 模型下二次启动只把
	// 已有窗口带到前台，不再开新窗。
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		app.focus()
	})
	mygo.App.WhenReady(func() {
		openAppWindow()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
