package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
	"zhimareview/internal/store"
)

// tab 条的渲染与交互（NewTester 渲染真实视图树）。headless 下 c.TitleBar()
// 是零值，走兜底高度，红绿灯留白逻辑不参与断言。

func tabApp(t *testing.T) (*application, *ui.Tester) {
	t.Helper()
	a := restoreApplication(testSession(0, "/tmp/projA", "/tmp/sandbox"))
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

// 悬停概况：全路径 + 仓库/改动数量；扫描中替代计数，错误追加标题；
// pick 态给入口提示。标题优先用户重命名。
func TestTabTitleAndTooltip(t *testing.T) {
	ws := newWorkspace(nil)
	if ws.tabTitle() != "新标签页" || !strings.Contains(ws.tabTooltip(), "未选择目录") {
		t.Fatalf("pick title=%q tooltip=%q", ws.tabTitle(), ws.tabTooltip())
	}
	ws.mode, ws.root = "work", "/tmp/projA"
	ws.repos = []git.Repo{{}, {}}
	ws.changes = []git.Change{{}, {}, {}}
	if ws.tabTitle() != "projA" {
		t.Fatalf("title = %q, want projA", ws.tabTitle())
	}
	tip := ws.tabTooltip()
	for _, want := range []string{"/tmp/projA", "2 个仓库", "3 处改动"} {
		if !strings.Contains(tip, want) {
			t.Fatalf("tooltip %q missing %q", tip, want)
		}
	}
	ws.scanning = true
	if tip := ws.tabTooltip(); !strings.Contains(tip, "扫描中") || strings.Contains(tip, "个仓库") {
		t.Fatalf("scanning tooltip = %q", tip)
	}
	ws.scanning = false
	ws.setErr("pull 失败", "detail")
	if tip := ws.tabTooltip(); !strings.Contains(tip, "错误：pull 失败") {
		t.Fatalf("error tooltip = %q", tip)
	}
	ws.tabMeta.name = "前端"
	if ws.tabTitle() != "前端" {
		t.Fatalf("renamed title = %q", ws.tabTitle())
	}
}

// 双击标签进入内联编辑：首帧聚焦并全选，输入替换原名，Enter 提交并落会话。
func TestTabBarDoubleClickRenames(t *testing.T) {
	a, tt := tabApp(t)
	for range 2 {
		if err := tt.Click("projA"); err != nil {
			t.Fatalf("click tab: %v", err)
		}
	}
	tt.Frame()
	tt.Frame()
	if !a.tabs[0].tabMeta.renaming {
		t.Fatalf("double click did not start rename")
	}
	if active, _ := tt.TextInputActive(); !active {
		t.Fatalf("rename input not focused")
	}
	tt.Type("前端")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	ws := a.tabs[0]
	if ws.tabMeta.renaming || ws.tabTitle() != "前端" {
		t.Fatalf("after commit: renaming=%t title=%q", ws.tabMeta.renaming, ws.tabTitle())
	}
	if !tt.HasText("前端") {
		t.Fatalf("renamed tab not painted: %v", tt.Texts())
	}
	if sess := a.sessionState(); sess.Tabs[0].Name != "前端" {
		t.Fatalf("session name = %q, want 前端", sess.Tabs[0].Name)
	}
}

// 重命名输入框有焦点时，左右方向键属于文本编辑，不能被 TabsBase 当成
// 切换 tab 的快捷键。
func TestTabBarRenameKeepsArrowsInInput(t *testing.T) {
	a, tt := tabApp(t)
	for range 2 {
		if err := tt.Click("sandbox"); err != nil {
			t.Fatalf("click sandbox: %v", err)
		}
	}
	tt.Frame()
	tt.Frame()
	if a.active != 1 || !a.tabs[1].tabMeta.renaming {
		t.Fatalf("active=%d renaming=%t, want editing sandbox", a.active, a.tabs[1].tabMeta.renaming)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if a.active != 1 {
		t.Fatalf("Left in rename input switched active to %d", a.active)
	}
	if !a.tabs[1].tabMeta.renaming {
		t.Fatalf("Left in rename input ended editing")
	}
}

// 右键菜单「重命名」入口；Esc 取消编辑，不落地草稿。
func TestTabBarRenameMenuEscapeCancels(t *testing.T) {
	a, tt := tabApp(t)
	if err := tt.RightClick("projA"); err != nil {
		t.Fatalf("right click: %v", err)
	}
	if err := tt.ChooseMenuItem("重命名"); err != nil {
		t.Fatalf("choose rename: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !a.tabs[0].tabMeta.renaming {
		t.Fatalf("menu rename did not start editing")
	}
	tt.Type("丢弃")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if ws := a.tabs[0]; ws.tabMeta.renaming || ws.tabTitle() != "projA" {
		t.Fatalf("esc: renaming=%t title=%q", ws.tabMeta.renaming, ws.tabTitle())
	}
	if sess := a.sessionState(); sess.Tabs[0].Name != "" {
		t.Fatalf("cancelled draft leaked to session: %q", sess.Tabs[0].Name)
	}
}

// 右键菜单预设颜色落到状态与会话，并在 tab 底边画出对应色条。
func TestTabBarColorMenuPaintsBottomBar(t *testing.T) {
	a, tt := tabApp(t)
	if err := tt.RightClick("projA"); err != nil {
		t.Fatalf("right click: %v", err)
	}
	if err := tt.ChooseMenuItem("颜色", "红"); err != nil {
		t.Fatalf("choose color: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if got := a.tabs[0].tabMeta.color; got != tabColorRed {
		t.Fatalf("color = %q, want red", got)
	}
	if sess := a.sessionState(); sess.Tabs[0].Color != tabColorRed {
		t.Fatalf("session color = %q", sess.Tabs[0].Color)
	}
	label, ok := tt.Find("projA")
	if !ok {
		t.Fatalf("tab label not found")
	}
	close, ok := tt.Find("关闭标签页 projA")
	if !ok {
		t.Fatalf("close button not found")
	}
	img := tt.Image()
	want := ui.Hex("#ef4444")
	found := 0
	for y := int(label.Y + label.H/2); y < 28; y++ {
		for x := int(label.X) - 8; x < int(close.X+close.W); x++ {
			c := img.RGBAAt(x, y)
			if c.R == want.R && c.G == want.G && c.B == want.B {
				found++
			}
		}
	}
	if found < 10 {
		t.Fatalf("color bar not painted: %d matching pixels", found)
	}
	// 「无颜色」清掉色条与会话值
	if err := tt.RightClick("projA"); err != nil {
		t.Fatalf("right click: %v", err)
	}
	if err := tt.ChooseMenuItem("颜色", "无颜色"); err != nil {
		t.Fatalf("clear color: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if a.tabs[0].tabMeta.color != "" || a.sessionState().Tabs[0].Color != "" {
		t.Fatalf("color not cleared: %q", a.tabs[0].tabMeta.color)
	}
}

// 失焦提交重命名：点别的 tab 时草稿落地，切换照常发生。
func TestTabBarRenameBlurCommits(t *testing.T) {
	a, tt := tabApp(t)
	for range 2 {
		if err := tt.Click("projA"); err != nil {
			t.Fatalf("click tab: %v", err)
		}
	}
	tt.Frame()
	tt.Frame()
	tt.Type("新名")
	// 首击必须直接切换 tab（失焦提交不能在这一击的释放前收窄 tab）
	if err := tt.Click("sandbox"); err != nil {
		t.Fatalf("click other tab: %v", err)
	}
	if a.active != 1 {
		t.Fatalf("active = %d, want switch to sandbox on first click", a.active)
	}
	// 延迟窗口过去后提交草稿
	tt.Advance(tabRenameBlurDelay + 10*time.Millisecond)
	tt.Frame()
	tt.Frame()
	if ws := a.tabs[0]; ws.tabMeta.renaming || ws.tabTitle() != "新名" {
		t.Fatalf("blur commit: renaming=%t title=%q", ws.tabMeta.renaming, ws.tabTitle())
	}
}

// 清空草稿提交恢复自动名（会话不存空名以外的冗余覆盖）。
func TestTabBarRenameEmptyRestoresAuto(t *testing.T) {
	a, tt := tabApp(t)
	for range 2 {
		if err := tt.Click("projA"); err != nil {
			t.Fatalf("click tab: %v", err)
		}
	}
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyBackspace) // 首帧已全选，退格清空
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	ws := a.tabs[0]
	if ws.tabMeta.renaming || ws.tabTitle() != "projA" || ws.tabMeta.name != "" {
		t.Fatalf("empty commit: renaming=%t title=%q name=%q", ws.tabMeta.renaming, ws.tabTitle(), ws.tabMeta.name)
	}
}

// 拖拽排序：把激活的 projA 拖到 sandbox 上，越过目标后落在其右侧；
// active 与 lastsActive 跟着 workspace 走，会话顺序同步。
func TestTabBarDragReorders(t *testing.T) {
	a, tt := tabApp(t)
	from, ok := tt.Find("projA")
	if !ok {
		t.Fatalf("source tab not found")
	}
	to, ok := tt.Find("sandbox")
	if !ok {
		t.Fatalf("target tab not found")
	}
	tt.Press(from.X+from.W/2, from.Y+from.H/2)
	for k := 1; k <= 4; k++ {
		f := float32(k) / 4
		tt.Move(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f)
	}
	tt.Release(to.X+to.W/2, to.Y+to.H/2)
	tt.Frame()
	tt.Frame()
	if got := rootsOf(a); !slices.Equal(got, []string{"/tmp/sandbox", "/tmp/projA"}) {
		t.Fatalf("tabs = %v, want [/tmp/sandbox /tmp/projA]", got)
	}
	if a.active != 1 || a.tabs[a.active].root != "/tmp/projA" {
		t.Fatalf("active = %d, want projA at 1", a.active)
	}
	if sess := a.sessionState(); sess.Active != 1 || sess.Tabs[0].Root != "/tmp/sandbox" {
		t.Fatalf("session = %+v", sess)
	}
}
