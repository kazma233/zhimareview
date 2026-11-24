package main

import (
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"zhimareview/internal/store"
)

// application 是应用壳：单窗口承载多个项目 tab，每 tab 一个 workspace。
// 窗口级状态（窗口、tab 集合、激活下标）归它，workspace 之间不共享可变
// 状态；跨 tab 共享的最近目录与会话（internal/store）也由它写。
type application struct {
	win *mygo.Window
	// post 把函数投递到主线程队列执行，bindWindow 绑定 win.Update；
	// nil（headless 测试）= 没有队列
	post   func(func())
	tabs   []*workspace
	active int
	// tabScroll 是 tab 滚动区的横向偏移（TrackScroll 契约：状态挂应用，
	// 跨帧保持滚动位置；tab 增删后越界由滚动容器钳回）
	tabScroll ui.ScrollState
	// lastActive 是上一次的激活下标：切 tab 时用来关掉旧 tab 的错误弹窗
	lastActive int
	// pendingCloses/pendingNew 收集本帧渲染中途的 ×/＋ 点击：构建中途改
	// a.tabs 会让同帧 range 循环与 TabsBase 的 n 越界（实抓 panic：12 个
	// tab 关一个，range 仍按旧长度 12 迭代 a.tabs[11]）；索引按帧开始时
	// 的快照记，tabBar 渲染完 flushTabOps 统一应用
	pendingCloses []int
	pendingNew    bool
	// pendingMoves 是本帧入队的 tab 重排。from/to 按 workspace 身份记录：
	// 关闭会改变下标，帧末应用时不能再相信入队时的位置。
	pendingMoves []tabMove
}

// tabMove 是一次重排：把 from 移到 to 所在的位置。
type tabMove struct {
	from *workspace
	to   *workspace
}

// app 是应用壳单例：单窗口模型下整个进程只有一个实例，由 openAppWindow
// 创建；headless 测试不经过它（自行构造 application）。
var app *application

// newApplication 构造应用壳并恢复上次会话。userDir 为空跳过会话读取
// （headless 测试没有应用运行时）。
func newApplication(userDir string) *application {
	var sess *store.Session
	if userDir != "" {
		sess = store.LoadSession(userDir)
	}
	return restoreApplication(sess)
}

// restoreApplication 按会话重建 tab 集合：每个 root 一个干净的 work 态
// workspace（只有根目录，扫描与监听由 openAppWindow 统一启动），激活项
// 越界归 0；无会话/空会话落单个选择态 tab。
func restoreApplication(sess *store.Session) *application {
	a := &application{}
	if sess != nil {
		for _, tab := range sess.Tabs {
			ws := a.newTabWorkspace()
			ws.root = tab.Root
			ws.mode = "work"
			ws.tabMeta.name = tab.Name
			ws.tabMeta.color = tab.Color
			a.tabs = append(a.tabs, ws)
		}
		if n := len(a.tabs); n > 0 {
			a.active = sess.Active
			if a.active < 0 || a.active >= n {
				a.active = 0
			}
		}
	}
	if len(a.tabs) == 0 {
		a.tabs = append(a.tabs, a.newTabWorkspace())
		a.active = 0
	}
	return a
}

// openAppWindow 创建单例窗口。win 必须在首帧渲染前就绪：NewWindow 同步
// 返回，view 首帧在事件循环里才构建。
func openAppWindow() {
	app = newApplication(userDataDir())
	setupAppMenu()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     "zhimareview",
		Width:     1180,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		// 隐藏系统标题栏但保留红绿灯：tab 条自绘在标题栏区（tabbar.go）。
		// 不用 HiddenInset：macOS 26 上它会让 mygo 挂空 NSToolbar
		//（darwin/window.go），系统给 toolbar 画的材质盖在自绘内容上，
		// 出现宽度异常的色块截断；Hidden 无 toolbar，标题栏区完全归应用画。
		TitleBarStyle: mygo.TitleBarHidden,
		// Win/Linux 的应用菜单栏默认藏起来（Alt 呼出），快捷键全程生效，
		// 不占自绘标题栏的布局；macOS 菜单属于应用，无窗口菜单栏，此选项
		// 不参与
		AutoHideMenuBar: true,
		// 同一类窗口记住各自的位置与大小。
		StateKey: "workspace",
		Content:  ui.View(app.view),
	})
	app.bindWindow(win)
	win.OnClosed(func() {
		for _, ws := range app.tabs {
			ws.stopWatch()
		}
	})
	for _, ws := range app.tabs {
		if ws.mode == "work" {
			ws.startWatch()
			// 记下重扫时间：随后的「窗口获得焦点」不再重复扫一遍
			ws.lastRefresh = time.Now()
			ws.rescan(ws.root)
		} else {
			ws.loadRecent()
		}
	}
	// 窗口获得焦点时刷新当前 tab：外部 git 操作（commit / add / stash）
	// 只改 .git 内部状态，被监听的路径没有事件，界面不会自己更新——这类
	// 操作多在应用之外（终端 / IDE）发生，用户切回来时刷新一次
	win.OnFocus(func() {
		if ws := app.activeTab(); ws != nil {
			ws.refreshOnDemand()
		}
	})
	app.syncTitle()
}

// userDataDir 读取用户数据目录；读不到返回空串，调用方降级（无会话恢复、
// 无最近列表持久化）。App.Path 只是加锁读配置，不 hop 主线程队列。
func userDataDir() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return dir
}

// bindWindow 把窗口与主线程队列接到应用壳与全部既有 tab 上。
func (a *application) bindWindow(win *mygo.Window) {
	a.win = win
	a.post = win.Update
	for _, ws := range a.tabs {
		ws.post = a.post
	}
}

// newTabWorkspace 构造挂到本应用的选择态 workspace，队列随绑定点同步。
func (a *application) newTabWorkspace() *workspace {
	ws := newWorkspace(a)
	ws.post = a.post
	return ws
}

// newTab 打开一个新的选择态 tab 并激活；已有选择态 tab 时直接激活它，
// 避免 pick tab 堆叠。
func (a *application) newTab() {
	for i, ws := range a.tabs {
		if ws.mode == "pick" {
			a.active = i
			a.tabChanged()
			return
		}
	}
	ws := a.newTabWorkspace()
	ws.loadRecent()
	a.tabs = append(a.tabs, ws)
	a.active = len(a.tabs) - 1
	a.tabChanged()
}

// closeTab 关闭第 i 个 tab：停监听、移除。关掉的是最后一个 tab 时补一个
// 选择态 tab（回到「新标签页」），不退出应用；激活项随移除收缩。
func (a *application) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	a.tabs[i].stopWatch()
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	switch {
	case len(a.tabs) == 0:
		ws := a.newTabWorkspace()
		ws.loadRecent()
		a.tabs = []*workspace{ws}
		a.active = 0
	case a.active > i:
		a.active--
	case a.active >= len(a.tabs):
		a.active = len(a.tabs) - 1
	}
	a.tabChanged()
}

// tabChanged 是 tab 集合或激活项变化后的统一钩子：窗口标题与会话跟随。
func (a *application) tabChanged() {
	// 切走时关掉旧 tab 的错误弹窗并标记已读：切回来不再弹（红点也消失），
	// 错误内容仍留在该 tab 上
	if a.lastActive != a.active {
		if old := a.tabAt(a.lastActive); old != nil {
			old.closeErr()
		}
		a.lastActive = a.active
		// 切到新 tab：刷新它。外部 git 操作（commit/add/stash）只改 .git
		// 内部状态，watch 收不到事件，切过来时补一次重扫
		if ws := a.activeTab(); ws != nil {
			ws.refreshOnDemand()
		}
	}
	a.syncTitle()
	a.saveSession()
}

// tabAt 返回第 i 个 tab；越界返回 nil。
func (a *application) tabAt(i int) *workspace {
	if i < 0 || i >= len(a.tabs) {
		return nil
	}
	return a.tabs[i]
}

// queueCloseTab/queueNewTab 是帧内 ×/＋ 点击的入口：只入队不改 a.tabs，
// 由 tabBar 渲染完的 flushTabOps 统一应用（帧内直接改会同帧越界，见
// pendingCloses 注释）。
func (a *application) queueCloseTab(i int) {
	a.pendingCloses = append(a.pendingCloses, i)
}

// closeActiveTab 是 Cmd/Ctrl+W（应用菜单「关闭标签页」）的入口：与 × 按钮
// 同一条帧安全路径，只入队，由下一帧 flushTabOps 统一应用；应用菜单 Click
// 不在帧构建中途，但复用入队路径让 tab 增删只有 flush 一个应用点。
func (a *application) closeActiveTab() {
	a.queueCloseTab(a.active)
	a.invalidate()
}

func (a *application) queueNewTab() {
	a.pendingNew = true
}

// queueMoveTab 入队一次拖拽重排；应用在帧末（flushTabOps），与关闭/新建
// 共用唯一落地口，构建中途不改 a.tabs。
func (a *application) queueMoveTab(from, to *workspace) {
	if from == nil || to == nil || from == to {
		return
	}
	a.pendingMoves = append(a.pendingMoves, tabMove{from: from, to: to})
}

// moveTab 把 from 放到 to 当前所在的位置。删除源后直接在目标的原下标
// 插入：向右拖过目标落在目标右侧，向左拖过目标落在目标左侧（越过语义）。
// active/lastActive 按 workspace 身份重新定位，保证选中的仍是同一个 tab。
func (a *application) moveTab(fromWS, toWS *workspace) {
	from := slices.Index(a.tabs, fromWS)
	to := slices.Index(a.tabs, toWS)
	if from < 0 || to < 0 || from == to {
		return
	}
	activeWS, lastWS := a.activeTab(), a.tabAt(a.lastActive)
	moved := a.tabs[from]
	a.tabs = append(a.tabs[:from], a.tabs[from+1:]...)
	a.tabs = slices.Insert(a.tabs, to, moved)
	if activeWS != nil {
		a.active = slices.Index(a.tabs, activeWS)
	}
	if lastWS != nil {
		a.lastActive = slices.Index(a.tabs, lastWS)
	}
}

// flushTabOps 应用本帧入队的 tab 增删与重排。关闭按降序执行：入队索引
// 都是帧开始时的快照，先删大索引不会让小索引失效；重排按 workspace 身份
// 落地，所以放在关闭/新建之后也不会错位。flush 后本帧剩余构建（内容区）
// 读的是新 tab 集合；TabsBase 本帧仍按旧 n 建了 tab 元素，越界的 ids
// 槽位为空、选中项由下一帧的 len 钳回，框架侧安全。
func (a *application) flushTabOps() {
	if len(a.pendingCloses) == 0 && !a.pendingNew && len(a.pendingMoves) == 0 {
		return
	}
	for i := len(a.pendingCloses) - 1; i >= 0; i-- {
		a.closeTab(a.pendingCloses[i])
	}
	a.pendingCloses = a.pendingCloses[:0]
	if a.pendingNew {
		a.pendingNew = false
		a.newTab()
	}
	if len(a.pendingMoves) > 0 {
		for _, mv := range a.pendingMoves {
			a.moveTab(mv.from, mv.to)
		}
		a.pendingMoves = a.pendingMoves[:0]
		// 关闭/新建会经 tabChanged 落盘；只有移动时补一次
		a.saveSession()
	}
}

// activeTab 返回激活 tab；越界防御（closeTab/newTab 之外没有写点）。
func (a *application) activeTab() *workspace {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

// view 是窗口根视图：自绘标题栏 tab 条 + 激活 tab 的内容（pick/work
// 两态路由在 workspace.view）。占「剩余高度」必须 Grow 而非 Fill
// （Fill=父容器 100%，叠加 tab 条行后基数超限，见 docs 坑记录）。
func (a *application) view(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		tabBar(c, a)
		ui.Column(c).FillWidth().Grow(1).Children(func() {
			if ws := a.activeTab(); ws != nil {
				ws.view(c)
			}
		})
		// 错误弹窗盖在整窗之上：错误归产生它的 tab，切到该 tab 时才弹
		if ws := a.activeTab(); ws != nil {
			errDialog(c, ws)
		}
	})
}

// syncTitle 让窗口标题跟随激活 tab：work 态是项目目录名，pick 态是应用名。
// MyGo 的窗口调用会把任务排进主线程队列再返回；view 帧与 Update 回调
// 本身就在主线程队列上执行，帧内同步调用 = 排队等自己，必然死锁。
// 新 goroutine 里调用则排到当前帧之后执行，安全。
func (a *application) syncTitle() {
	if a == nil || a.win == nil {
		return
	}
	title := "zhimareview"
	if ws := a.activeTab(); ws != nil {
		switch {
		case ws.tabMeta.name != "":
			title = ws.tabMeta.name
		case ws.mode == "work":
			title = filepath.Base(ws.root)
		}
	}
	go a.win.SetTitle(title)
}

// sessionState 快照当前会话：work 态 tab 按序入列（含重命名/颜色），pick
// 态不入；激活项换成过滤后列表里的下标（激活的是 pick tab 时为 -1，下次
// 启动落在第一个项目）。纯读快照，供 saveSession 与测试使用。
func (a *application) sessionState() store.Session {
	var tabs []store.Tab
	active := -1
	for i, ws := range a.tabs {
		if ws.mode != "work" {
			continue
		}
		if i == a.active {
			active = len(tabs)
		}
		tabs = append(tabs, store.Tab{
			Root:  ws.root,
			Name:  ws.tabMeta.name,
			Color: ws.tabMeta.color,
		})
	}
	return store.Session{Tabs: tabs, Active: active}
}

// saveSession 把会话快照落盘（goroutine 写，不占主线程）。没有窗口
// （headless 测试）不写，测试对会话的断言走 sessionState 与 store 层。
func (a *application) saveSession() {
	if a == nil || a.win == nil {
		return
	}
	sess := a.sessionState()
	go func() {
		if dir := userDataDir(); dir != "" {
			_ = store.SaveSession(dir, sess)
		}
	}()
}

// focus 把已有窗口带到前台（二次启动应用时）。窗口调用 hop 主线程队列，
// 从回调走 goroutine 排到队列之后（同 syncTitle 的坑）。
func (a *application) focus() {
	if a == nil || a.win == nil {
		return
	}
	go a.win.Focus()
}

// invalidate 请求重绘；nil 接收与 headless（无窗口）安全。
func (a *application) invalidate() {
	if a != nil && a.win != nil {
		a.win.Invalidate()
	}
}
