package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"zhimareview/internal/store"
)

// 应用菜单（appmenu.go）：Cmd/Ctrl+W 关标签页、Cmd/Ctrl+Q 退出的模板接线
// 与点击行为。SetMenu 在应用未 ready 时只存字段不触达后端，模板构建是纯
// 内存操作，headless 可单测。

// fileItems 取模板里的「文件」子菜单项集合；没有时测试失败。
func fileItems(t *testing.T, m *mygo.Menu) []*mygo.MenuItem {
	t.Helper()
	for _, it := range m.Items() {
		if it.Label == "文件" {
			return it.Submenu
		}
	}
	t.Fatalf("no file menu in template")
	return nil
}

// closeTabItem 取「关闭标签页」项；没有时测试失败。
func closeTabItem(t *testing.T, m *mygo.Menu) *mygo.MenuItem {
	t.Helper()
	for _, it := range fileItems(t, m) {
		if it.Label == "关闭标签页" {
			return it
		}
	}
	t.Fatalf("no close-tab item in file menu")
	return nil
}

// macOS 模板：Cmd+W 归关闭标签页；退出由 App 菜单（role 子菜单）自带，
// 文件菜单不出现第二条退出项。
func TestAppMenuMac(t *testing.T) {
	m := appMenu(false)
	it := closeTabItem(t, m)
	if it.Accelerator != "CmdOrCtrl+W" || it.Click == nil {
		t.Fatalf("close tab item: accel %q click nil %t", it.Accelerator, it.Click == nil)
	}
	for _, it := range fileItems(t, m) {
		if it.Role == mygo.RoleQuit {
			t.Fatalf("quit role leaked into file menu on mac")
		}
	}
}

// 其余平台模板：退出项显式带 Ctrl+Q——RoleQuit 在 Windows 上默认没有快捷键。
func TestAppMenuNonMac(t *testing.T) {
	m := appMenu(true)
	var quit *mygo.MenuItem
	for _, it := range fileItems(t, m) {
		if it.Role == mygo.RoleQuit {
			quit = it
		}
	}
	if quit == nil || quit.Accelerator != "CmdOrCtrl+Q" {
		t.Fatalf("quit item missing or wrong accel: %+v", quit)
	}
}

// 「关闭标签页」点击走帧安全入队路径：不直接改 tab 集合，下一帧 flush
// 时才应用。
func TestAppMenuCloseTabClick(t *testing.T) {
	a := restoreApplication(&store.Session{Roots: []string{"/tmp/projA", "/tmp/sandbox"}, Active: 0})
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	tt.Frame()

	old := app
	app = a
	defer func() { app = old }()
	it := closeTabItem(t, appMenu(false))
	it.Click(it, nil)
	if len(a.pendingCloses) != 1 || a.pendingCloses[0] != a.active {
		t.Fatalf("close not queued: %v", a.pendingCloses)
	}
	if len(a.tabs) != 2 {
		t.Fatalf("tabs changed before flush")
	}
	tt.Frame()
	tt.Frame()
	if len(a.tabs) != 1 || a.tabs[0].root != "/tmp/sandbox" {
		t.Fatalf("after flush: %d tabs, first root %q", len(a.tabs), a.tabs[0].root)
	}
}
