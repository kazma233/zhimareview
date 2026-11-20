package main

import (
	"testing"

	"zhimareview/internal/store"
)

// 应用壳的 tab 生命周期与会话快照契约（headless：win/post 为 nil，
// tabChanged 的标题同步与落盘钩子自动跳过，断言走纯状态）。

func workApp(roots ...string) *application {
	a := restoreApplication(&store.Session{Roots: roots, Active: len(roots) - 1})
	return a
}

// 关最后一个项目 tab → 回选择态（新标签页），不退出应用。
func TestCloseTabLastGoesPick(t *testing.T) {
	a := workApp("/a")
	a.closeTab(0)
	if len(a.tabs) != 1 || a.tabs[0].mode != "pick" {
		t.Fatalf("tabs = %d, mode = %q; want 1 pick tab", len(a.tabs), a.tabs[0].mode)
	}
	if a.active != 0 {
		t.Errorf("active = %d, want 0", a.active)
	}
}

// 关掉激活项之前的 tab → 激活项随移除左移，仍指向原 tab。
func TestCloseTabActiveShifts(t *testing.T) {
	a := workApp("/a", "/b", "/c")
	a.active = 2
	a.closeTab(0)
	if len(a.tabs) != 2 || a.active != 1 || a.tabs[1].root != "/c" {
		t.Fatalf("tabs = %d, active = %d, tabs[1].root = %q", len(a.tabs), a.active, a.tabs[1].root)
	}
}

// 关掉末尾的激活 tab → 激活项收缩到新的末尾。
func TestCloseTabActiveClamps(t *testing.T) {
	a := workApp("/a", "/b")
	a.closeTab(1)
	if len(a.tabs) != 1 || a.active != 0 || a.tabs[0].root != "/a" {
		t.Fatalf("tabs = %d, active = %d", len(a.tabs), a.active)
	}
}

// 会话快照：pick 态 tab 不入列，激活项换成过滤后下标；激活 pick tab 时为 -1。
func TestSessionStateFiltersPick(t *testing.T) {
	a := workApp("/a", "/b")
	a.tabs = append(a.tabs, a.newTabWorkspace()) // 尾部 pick tab
	a.active = 2                                 // 激活的是 pick tab
	sess := a.sessionState()
	if len(sess.Roots) != 2 || sess.Roots[0] != "/a" || sess.Roots[1] != "/b" || sess.Active != -1 {
		t.Fatalf("session = %+v, want [/a /b] active -1", sess)
	}
	a.active = 1
	if sess := a.sessionState(); sess.Active != 1 {
		t.Fatalf("session = %+v, want active 1", sess)
	}
}

// 会话恢复：roots 重建为干净的 work 态 tab（顺序保持、激活项对位）。
func TestRestoreApplication(t *testing.T) {
	a := restoreApplication(&store.Session{Roots: []string{"/a", "/b", "/c"}, Active: 1})
	if len(a.tabs) != 3 || a.active != 1 {
		t.Fatalf("tabs = %d, active = %d; want 3 tabs active 1", len(a.tabs), a.active)
	}
	for i, root := range []string{"/a", "/b", "/c"} {
		if a.tabs[i].mode != "work" || a.tabs[i].root != root {
			t.Fatalf("tab %d = %q (%q), want work %q", i, a.tabs[i].mode, a.tabs[i].root, root)
		}
	}
	// 激活项越界归 0；无会话落单个选择态 tab
	if a := restoreApplication(&store.Session{Roots: []string{"/a"}, Active: 5}); a.active != 0 {
		t.Errorf("active = %d, want clamped 0", a.active)
	}
	if a := restoreApplication(nil); len(a.tabs) != 1 || a.tabs[0].mode != "pick" {
		t.Fatalf("restore(nil) = %d tabs, want 1 pick", len(a.tabs))
	}
}

// ＋ 按钮：无 pick tab 时新建并激活；已有 pick tab 时原地激活不堆叠。
func TestNewTabReusesPick(t *testing.T) {
	a := workApp("/a")
	a.newTab()
	if len(a.tabs) != 2 || a.tabs[1].mode != "pick" || a.active != 1 {
		t.Fatalf("after newTab: %d tabs, active %d; want 2 tabs active 1", len(a.tabs), a.active)
	}
	a.newTab()
	if len(a.tabs) != 2 || a.active != 1 {
		t.Fatalf("second newTab stacked: %d tabs, active %d; want reuse", len(a.tabs), a.active)
	}
}

// 帧内入队的关闭按降序应用：入队索引是帧开始时的快照，先删大索引不让
// 小索引失效；flush 后 active 收缩到合法区间。
func TestFlushTabOpsDescending(t *testing.T) {
	a := workApp("/a", "/b", "/c", "/d", "/e")
	a.active = 4
	a.queueCloseTab(1)
	a.queueCloseTab(3)
	a.flushTabOps()
	if len(a.tabs) != 3 {
		t.Fatalf("tabs = %d, want 3", len(a.tabs))
	}
	for i, root := range []string{"/a", "/c", "/e"} {
		if a.tabs[i].root != root {
			t.Fatalf("tabs[%d].root = %q, want %q", i, a.tabs[i].root, root)
		}
	}
	if a.active != 2 || a.tabs[a.active].root != "/e" {
		t.Fatalf("active = %d root = %q, want 2 /e", a.active, a.tabs[a.active].root)
	}
}

// 帧内入队的 ＋：flush 后生效（真实触发点在 tabBar 渲染完，见 tabbar_test）。
func TestFlushTabOpsNew(t *testing.T) {
	a := workApp("/a")
	a.queueNewTab()
	if len(a.tabs) != 1 {
		t.Fatalf("queueNewTab must not mutate before flush; tabs = %d", len(a.tabs))
	}
	a.flushTabOps()
	if len(a.tabs) != 2 || a.tabs[1].mode != "pick" || a.active != 1 {
		t.Fatalf("after flush: %d tabs, active %d, mode %q", len(a.tabs), a.active, a.tabs[1].mode)
	}
}
