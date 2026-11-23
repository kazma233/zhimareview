package main

import (
	"slices"
	"testing"

	"zhimareview/internal/store"
)

// 应用壳的 tab 生命周期与会话快照契约（headless：win/post 为 nil，
// tabChanged 的标题同步与落盘钩子自动跳过，断言走纯状态）。

// testSession 用根目录构造会话；元数据测试直接构造 store.Tab。
func testSession(active int, roots ...string) *store.Session {
	sess := &store.Session{Active: active}
	for _, root := range roots {
		sess.Tabs = append(sess.Tabs, store.Tab{Root: root})
	}
	return sess
}

func workApp(roots ...string) *application {
	a := restoreApplication(testSession(len(roots)-1, roots...))
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
	if len(sess.Tabs) != 2 || sess.Tabs[0].Root != "/a" || sess.Tabs[1].Root != "/b" || sess.Active != -1 {
		t.Fatalf("session = %+v, want [/a /b] active -1", sess)
	}
	a.active = 1
	if sess := a.sessionState(); sess.Active != 1 {
		t.Fatalf("session = %+v, want active 1", sess)
	}
}

// 会话恢复：roots 重建为干净的 work 态 tab（顺序保持、激活项对位）。
func TestRestoreApplication(t *testing.T) {
	a := restoreApplication(testSession(1, "/a", "/b", "/c"))
	if len(a.tabs) != 3 || a.active != 1 {
		t.Fatalf("tabs = %d, active = %d; want 3 tabs active 1", len(a.tabs), a.active)
	}
	for i, root := range []string{"/a", "/b", "/c"} {
		if a.tabs[i].mode != "work" || a.tabs[i].root != root {
			t.Fatalf("tab %d = %q (%q), want work %q", i, a.tabs[i].mode, a.tabs[i].root, root)
		}
	}
	// 激活项越界归 0；无会话落单个选择态 tab
	if a := restoreApplication(testSession(5, "/a")); a.active != 0 {
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

// 拖拽重排的落点语义：删除源后直接在目标原下标插入——向右拖过目标落在
// 其右侧，向左拖过目标落在其左侧。
func TestMoveTabAcrossTarget(t *testing.T) {
	a := workApp("/a", "/b", "/c", "/d")
	a.active, a.lastActive = 0, 0
	a.moveTab(a.tabs[0], a.tabs[2]) // /a 拖到 /c 上
	if got := rootsOf(a); !slices.Equal(got, []string{"/b", "/c", "/a", "/d"}) {
		t.Fatalf("rightward move = %v, want [/b /c /a /d]", got)
	}
	if a.active != 2 || a.tabs[a.active].root != "/a" || a.lastActive != 2 {
		t.Fatalf("active = %d last = %d, want both on /a at 2", a.active, a.lastActive)
	}
	a.moveTab(a.tabs[3], a.tabs[0]) // /d 拖到 /b 上
	if got := rootsOf(a); !slices.Equal(got, []string{"/d", "/b", "/c", "/a"}) {
		t.Fatalf("leftward move = %v, want [/d /b /c /a]", got)
	}
	if a.active != 3 || a.tabs[a.active].root != "/a" {
		t.Fatalf("active = %d root = %q, want /a at 3", a.active, a.tabs[a.active].root)
	}
}

// 重排只换顺序、不换身份：active/lastActive 各自跟随原来的 workspace。
func TestMoveTabKeepsDistinctSelections(t *testing.T) {
	a := workApp("/a", "/b", "/c")
	a.active, a.lastActive = 2, 0 // 正在 /c，上一个 /a
	a.moveTab(a.tabs[0], a.tabs[2])
	if a.active != 1 || a.tabs[a.active].root != "/c" {
		t.Fatalf("active = %d, want /c at 1", a.active)
	}
	if a.lastActive != 2 || a.tabs[a.lastActive].root != "/a" {
		t.Fatalf("lastActive = %d, want /a at 2", a.lastActive)
	}
}

// 关闭与重排同帧入队：先按索引快照关闭，再按 workspace 身份重排，不串位。
func TestFlushTabOpsCloseThenMove(t *testing.T) {
	a := workApp("/a", "/b", "/c")
	a.active, a.lastActive = 0, 0
	a.queueCloseTab(2)
	a.queueMoveTab(a.tabs[0], a.tabs[1])
	a.flushTabOps()
	if got := rootsOf(a); !slices.Equal(got, []string{"/b", "/a"}) {
		t.Fatalf("tabs = %v, want [/b /a]", got)
	}
	if a.active != 1 || a.tabs[a.active].root != "/a" {
		t.Fatalf("active = %d, want /a at 1", a.active)
	}
}

// 会话按 tab 顺序保存名称/颜色；pick tab 仍不入列。
func TestSessionStateCarriesTabMeta(t *testing.T) {
	a := workApp("/a", "/b")
	a.tabs[0].tabMeta.name = "前端"
	a.tabs[0].tabMeta.color = tabColorRed
	a.tabs[1].tabMeta.color = tabColorBlue
	pick := a.newTabWorkspace()
	pick.tabMeta.name = "临时"
	a.tabs = append(a.tabs, pick)
	a.active = 0
	sess := a.sessionState()
	if len(sess.Tabs) != 2 {
		t.Fatalf("session tabs = %d, want 2", len(sess.Tabs))
	}
	if sess.Tabs[0] != (store.Tab{Root: "/a", Name: "前端", Color: tabColorRed}) {
		t.Fatalf("tabs[0] = %+v", sess.Tabs[0])
	}
	if sess.Tabs[1] != (store.Tab{Root: "/b", Color: tabColorBlue}) {
		t.Fatalf("tabs[1] = %+v", sess.Tabs[1])
	}
}

// 会话恢复回填名称/颜色，tabTitle 优先自定义名。
func TestRestoreTabMeta(t *testing.T) {
	a := restoreApplication(&store.Session{
		Tabs:   []store.Tab{{Root: "/tmp/projA", Name: "前端", Color: tabColorGreen}},
		Active: 0,
	})
	ws := a.tabs[0]
	if ws.tabTitle() != "前端" || ws.tabMeta.color != tabColorGreen {
		t.Fatalf("restored title %q color %q", ws.tabTitle(), ws.tabMeta.color)
	}
	ws.tabMeta.name = ""
	if ws.tabTitle() != "projA" {
		t.Fatalf("fallback title = %q, want projA", ws.tabTitle())
	}
}

func rootsOf(a *application) []string {
	roots := make([]string, len(a.tabs))
	for i, ws := range a.tabs {
		roots[i] = ws.root
	}
	return roots
}
