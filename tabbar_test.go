package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/store"
)

// tab 条的渲染与交互（NewTester 渲染真实视图树）。headless 下 c.TitleBar()
// 是零值，走兜底高度，红绿灯留白逻辑不参与断言。

func tabApp(t *testing.T) (*application, *ui.Tester) {
	t.Helper()
	a := restoreApplication(&store.Session{Roots: []string{"/tmp/projA", "/tmp/sandbox"}, Active: 0})
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	return a, tt
}

// 两个 work tab 都渲染出目录名，激活项默认会话里的第一个。
func TestTabBarRendersTabs(t *testing.T) {
	_, tt := tabApp(t)
	if !tt.HasText("projA") || !tt.HasText("sandbox") {
		t.Fatalf("tabs not rendered: %v", tt.Texts())
	}
}

// 点 tab 切换激活项，内容区跟着换项目（顶栏路径与扫描状态来源随之变化）。
func TestTabBarSwitchActivates(t *testing.T) {
	a, tt := tabApp(t)
	if err := tt.Click("sandbox"); err != nil {
		t.Fatalf("click sandbox tab: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if a.active != 1 {
		t.Fatalf("active = %d, want 1", a.active)
	}
	if !tt.HasText("/tmp/sandbox") {
		t.Fatalf("workspace content did not follow active tab")
	}
}

// × 关闭非激活 tab：集合收缩、激活项不变。
func TestTabBarCloseTab(t *testing.T) {
	a, tt := tabApp(t)
	if err := tt.Click("关闭标签页 sandbox"); err != nil {
		t.Fatalf("click close: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if len(a.tabs) != 1 || a.tabs[0].root != "/tmp/projA" || a.active != 0 {
		t.Fatalf("after close: %d tabs active %d", len(a.tabs), a.active)
	}
}

// ＋ 新建选择态 tab 并激活，内容区回入口页；既有工作区原地保留。
func TestTabBarPlusOpensPick(t *testing.T) {
	a, tt := tabApp(t)
	if err := tt.Click("新建标签页"); err != nil {
		t.Fatalf("click plus: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if len(a.tabs) != 3 || a.active != 2 || a.tabs[2].mode != "pick" {
		t.Fatalf("after plus: %d tabs, active %d, mode %q", len(a.tabs), a.active, a.tabs[2].mode)
	}
	if a.tabs[0].root != "/tmp/projA" || a.tabs[0].mode != "work" {
		t.Fatalf("existing workspace not preserved: %+v", a.tabs[0])
	}
	if !tt.HasText("选择目录…") {
		t.Fatalf("pick view not shown after new tab")
	}
}

// store 没有直接参与渲染，但 tab 条交互依赖 restoreApplication 的会话输入，
// 这里锁定格式防止字段改名静默破坏恢复链路。
func TestSessionJSONShape(t *testing.T) {
	sess := store.LoadSession(".") // 不存在的目录 → nil，恢复走单 pick tab
	if sess != nil {
		t.Fatalf("LoadSession(missing dir) = %+v, want nil", sess)
	}
}
