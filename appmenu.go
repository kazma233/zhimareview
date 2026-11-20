package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

// setupAppMenu 安装应用菜单：Cmd/Ctrl+W 关标签页、Cmd/Ctrl+Q 退出的分发
// 归属源。不设置时框架装默认菜单，macOS 上 Cmd+W 的语义是系统默认的
// 「关闭窗口」——单窗多 tab 模型下它归当前 tab；自定义菜单顶掉默认菜单
// 后 Cmd+W 才落到关闭标签页。CmdOrCtrl 由框架转换：macOS 是 Cmd，其余
// 平台是 Ctrl。菜单 Click 在主线程执行，与帧构建互斥。
func setupAppMenu() {
	mygo.App.SetMenu(appMenu(runtime.GOOS != "darwin"))
}

// appMenu 构建菜单模板。nonMac 抽成参数：退出项只属于带窗口菜单栏的
// 平台（macOS 的退出在 RoleAppMenu 自带的子菜单里，Cmd+Q），显式参数让
// 两个平台的模板都能单测。
func appMenu(nonMac bool) *mygo.Menu {
	file := []*mygo.MenuItem{
		{
			Label:       "关闭标签页",
			Accelerator: "CmdOrCtrl+W",
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				if app == nil {
					return
				}
				app.closeActiveTab()
			},
		},
	}
	if nonMac {
		// RoleQuit 的行为（App.Quit）由框架角色提供；显式 accelerator 覆盖
		// 默认值——RoleQuit 在 Windows 上默认没有快捷键
		file = append(file, mygo.Separator(), &mygo.MenuItem{
			Role:        mygo.RoleQuit,
			Accelerator: "CmdOrCtrl+Q",
		})
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		// mac 专属角色在其余平台自动隐藏（框架机制），一套模板服务全平台
		{Role: mygo.RoleAppMenu},
		{Label: "文件", Submenu: file},
		// 编辑角色把 Cmd/Ctrl+C/V/Z 等交给内容区：全局搜索框的文本编辑依赖
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	})
}
