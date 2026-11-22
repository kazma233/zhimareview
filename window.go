package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/egoist/mygo/ui/markdown"
	"zhimareview/internal/git"
	"zhimareview/internal/store"
)

// workspace 是一个 tab 的完整状态。单窗多 tab 模型下每个 tab 独立持有
// 一个 workspace，互不共享可变状态；跨 tab 共享的只有最近目录列表与会话
// （internal/store），归 application 编排。
// 结构按归属分区：根层是队列/两态/扫描模型与横切开关，四个视图分区
// （tree/diffv/hist/dirs，类型声明在各视图文件）各自只被自己的渲染与
// 派生写；跨分区写点以 ws.hist.xxx 的形式显式可见。
type workspace struct {
	// app 是应用壳（窗口与主线程队列的归属方）；nil（headless 测试）=
	// 没有窗口，窗口副作用跳过
	app *application
	// post 把函数投递到主线程队列执行，绑定点在 bindWindow/newTabWorkspace；
	// nil（headless 测试）= 没有队列，管线整条同步执行（见 loadAsync）
	post func(func())

	// mode 是 tab 的两态：选目录（pick）或工作区（work）。
	// pick 是初始态；选定目录后进入 work。
	mode   string
	root   string
	recent []string
	// err 是横切错误反馈：写点在各管线的失败分支（runWrite/各 apply），
	// 成功路径清空；以弹窗呈现（errdialog.go），不占布局
	err     *errInfo
	errOpen bool
	// errSeen 是错误是否已被用户看过：未读时 tab 上有红点标记，
	// 用户看过弹窗（关闭/切走 tab）后消失
	errSeen bool
	// lastRefresh 是上次重扫时间：切 tab / 窗口获得焦点的重扫按它节流
	lastRefresh time.Time
	scanning    bool
	// fetchingAll 是顶栏「全部 fetch」在途标记：与单仓库 fetch/pull 互斥，
	// 避免同一仓库的并发 git 写操作
	fetchingAll bool

	// 扫描模型：全工作区共享的数据；两条生产者写入（rescan 全量、watch 增量合并）
	repos   []git.Repo
	changes []git.Change
	// changeByAbs 是模型派生索引（abs = RepoPath/Path → 改动），随
	// rebuildDerived 重建（buildChangeByAbs），供目录模式与文件查看 O(1) 查表
	changeByAbs map[string]*git.Change
	activeRepo  string // 当前仓库：扫描默认值/历史页数据源/目录点击联动三方共享

	// 横切路由与开关
	mainTab     int    // 0=工作区 1=提交历史（全页切换）
	sidebarTab  string // changes / dir（侧栏切换）
	sidebarSize float32
	mdPreview   bool             // Markdown 预览（文件查看与 diff 共用开关）
	mdOpts      markdown.Options // Markdown 预览配置（LoadImage 随打开的文件更新）
	// diffSeg 是 diff 布局的分段选择（0=统一 1=分栏）：主 diff 与历史详情
	// 共用一个布局偏好；布局切换控件（layoutSwitch）直写此字段，
	// 两个视图渲染时各自经 diffLayout 换算成官方布局
	diffSeg     int
	watchCancel context.CancelFunc

	// 视图分区（值字段，永远在场；字段与其唯一写点见各分区类型）
	tree   changeTreePane
	diffv  diffPane
	hist   historyPane
	dirs   dirPane
	search searchPane
}

// newWorkspace 构造初始 workspace（选择态）。侧栏选区经 newSelList
// （-1 起步，坑 11：零值 0 会让列表首行凭空选中并卡死滚轮）；历史页选区
// 是自成一套的 ui.List（非 sideRow 渲染），初值在此一并集中。
func newWorkspace(app *application) *workspace {
	return &workspace{
		app:         app,
		mode:        "pick",
		sidebarSize: 300,
		tree:        changeTreePane{list: newSelList(), flatList: newSelList()},
		hist:        historyPane{split: 500, sel: -1},
		dirs:        dirPane{list: newSelList()},
		search:      searchPane{selList: newSelList()},
	}
}

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
		for _, root := range sess.Roots {
			ws := a.newTabWorkspace()
			ws.root = root
			ws.mode = "work"
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

// flushTabOps 应用本帧入队的 tab 增删。关闭按降序执行：入队索引都是帧
// 开始时的快照，先删大索引不会让小索引失效。flush 后本帧剩余构建（内容
// 区）读的是新 tab 集合；TabsBase 本帧仍按旧 n 建了 tab 元素，越界的
// ids 槽位为空、选中项由下一帧的 len 钳回，框架侧安全。
func (a *application) flushTabOps() {
	if len(a.pendingCloses) == 0 && !a.pendingNew {
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
	if ws := a.activeTab(); ws != nil && ws.mode == "work" {
		title = filepath.Base(ws.root)
	}
	go a.win.SetTitle(title)
}

// sessionState 快照当前会话：work 态 tab 的根目录按序入列，pick 态不入；
// 激活项换成过滤后列表里的下标（激活的是 pick tab 时为 -1，下次启动落在
// 第一个项目）。纯读快照，供 saveSession 与测试使用。
func (a *application) sessionState() store.Session {
	var roots []string
	active := -1
	for i, ws := range a.tabs {
		if ws.mode != "work" {
			continue
		}
		if i == a.active {
			active = len(roots)
		}
		roots = append(roots, ws.root)
	}
	return store.Session{Roots: roots, Active: active}
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

// loadRecent 异步读取最近目录列表；读不到就保持空，入口页不显示该区块。
// headless（post 未绑定）不读：测试直接注入 ws.recent。
func (ws *workspace) loadRecent() {
	if ws.post == nil {
		return
	}
	loadAsync(ws, func() []string {
		return store.LoadRecent(userDataDir())
	}, func(recent []string) {
		if len(recent) > 0 {
			ws.recent = recent
		}
	})
}

// chooseDir 进入工作区：换根目录、写最近列表、重新扫描、会话跟随。
// 只做状态变更与异步副作用，供 view 帧内和 Update 回调里调用——
// 帧内同步调用窗口 API（hop 主线程队列）会死锁，见 syncTitle。
func (ws *workspace) chooseDir(dir string) {
	ws.root = dir
	ws.mode = "work"
	ws.clearErr()
	ws.activeRepo = ""
	ws.app.syncTitle()
	ws.startWatch()
	ws.rescan(dir)
	ws.app.saveSession()

	go func() {
		if ws.app == nil {
			// headless 测试没有窗口，持久化行为由 store 层单测覆盖
			return
		}
		recent := store.RememberRecent(userDataDir(), dir)
		ws.post(func() { ws.recent = recent })
	}()
}

// browse 打开系统目录选择对话框；对话框同步阻塞，经管线离开主线程调用。
func (ws *workspace) browse() {
	loadAsync(ws, func() string {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:     "选择工作区根目录",
			Directory: true,
		})
		if err != nil || len(paths) == 0 {
			return ""
		}
		// 应用内路径统一 `/` 分隔：与 git 输出一致，避免两套分隔符比较失效。
		return filepath.ToSlash(paths[0])
	}, func(dir string) {
		if dir != "" {
			ws.chooseDir(dir)
		}
	})
}

// rescan 扫描根目录下的仓库并采集改动，完成后应用结果。
// 期间换目录或回到选择态时结果作废（applyScanState 守卫）。
func (ws *workspace) rescan(dir string) {
	ws.rescanWith(dir, nil)
}

// rescanWith 是 rescan 的带完成回调版本：扫描结果应用之后（即使被守卫
// 丢弃）在主线程执行 done，供调用方在 applyScanState 清空错误条之后再
// 落自己的错误信息。
func (ws *workspace) rescanWith(dir string, done func()) {
	if dir == "" {
		return
	}
	ws.scanning = true
	// 扫描可由对话框回调触发（不在渲染帧内），置旗标后主动请求重绘
	ws.app.invalidate()
	loadAsync(ws, func() scanResult {
		found := git.FindRepos(dir)
		return scanResult{found: found, changes: git.CollectChanges(found)}
	}, func(r scanResult) {
		ws.applyScanState(dir, r.found, r.changes)
		if done != nil {
			done()
		}
	})
}

// scanResult 是 rescan 管线的加载结果（仓库集合 + 全量改动）。
type scanResult struct {
	found   []git.Repo
	changes []git.Change
}

// fetchAllRepos 是顶栏「全部 fetch」：并发拉取工作区所有仓库的远程引用。
// 失败按仓库聚合反馈（最多列 3 个）；完成后历史失效（在历史页则立即重载）。
func (ws *workspace) fetchAllRepos() {
	if ws.fetchingAll || ws.hist.remoteBusy != "" || len(ws.repos) == 0 {
		return
	}
	root := ws.root
	repos := append([]git.Repo(nil), ws.repos...)
	ws.fetchingAll = true
	loadAsync(ws, func() []git.FetchResult { return git.FetchRepos(repos) },
		func(results []git.FetchResult) {
			ws.fetchingAll = false
			// 期间换了根目录或回到选择态：旧工作区的结果不落进新工作区
			if ws.root != root || ws.mode != "work" {
				return
			}
			var failed []string
			for _, r := range results {
				if r.Err == nil {
					continue
				}
				failed = append(failed, repoDisplayName(&r.Repo)+" — "+firstLine(r.Err.Error()))
			}
			switch {
			case len(failed) == 0:
				ws.clearErr()
			case len(failed) <= 3:
				ws.setErr(fmt.Sprintf("fetch 失败 %d/%d", len(failed), len(results)), strings.Join(failed, "\n"))
			default:
				ws.setErr(fmt.Sprintf("fetch 失败 %d/%d", len(failed), len(results)), strings.Join(failed[:3], "\n")+"\n等")
			}
			// 让历史失效：不在历史页时等进入页面重载，在页内则立即重读
			ws.reloadCommits()
		})
}

// firstLine 取错误文本首行：git 的多行提示压进单行错误条。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// applyScanState 应用扫描结果；root 已变或已回选择态时丢弃。仓库与改动都
// 和当前一致（例行重扫没扫出新东西）时只清过程状态，不动数据与派生——
// 重读-比对-落地，改动树/目录行的整表重建对用户是纯闪烁。
func (ws *workspace) applyScanState(dir string, found []git.Repo, changes []git.Change) {
	if ws.root != dir || ws.mode != "work" {
		return
	}
	changed := !slices.Equal(ws.repos, found) || !slices.Equal(ws.changes, changes)
	if changed {
		ws.repos = found
		ws.changes = changes
	}
	ws.scanning = false
	ws.clearErr()
	// 根目录变化时目录模式状态重置；改动树显式收起的目录按条目 id 记录
	//（tree.collapsed），跨扫描天然保持，无需重置
	if ws.root != ws.dirs.rootLoaded {
		ws.dirs.cache = map[string][]fsEntry{}
		ws.dirs.loading = map[string]bool{}
		ws.dirs.expanded = map[string]bool{}
		ws.dirs.rootLoaded = ws.root
	}
	// 默认仓库落在第一个有改动的仓库，其次第一个仓库
	if ws.activeRepo == "" || repoAt(ws.repos, ws.activeRepo) == nil {
		ws.activeRepo = firstRepoWithChanges(found, changes)
	}
	if changed {
		ws.rebuildDerived()
		ws.dirs.rebuildRows(ws.root, ws.changes)
	}
}

// rebuildDerived 重建派生数据：模型索引（changeByAbs）与各视图的派生
// （改动树/列表布局 tree、历史页仓库下拉 hist）。数据（repos/changes/
// activeRepo）归模型，派生归分区，本函数只做编排——从模型取数喂给
// 各纯输入派生函数。
func (ws *workspace) rebuildDerived() {
	ws.changeByAbs = buildChangeByAbs(ws.changes)
	ws.tree.rebuild(ws.repos, ws.changes)
	ws.hist.rebuildOptions(ws.repos, ws.activeRepo)
}

// buildChangeByAbs 建文件改动索引（abs = RepoPath/Path → 改动）。同文件
// 多分区（如暂存后又修改）只留一份：工作区侧优先（unstaged > staged >
// 其他）——「查看 diff」入口先看工作区状态。指针指向 changes 切片元素，
// 索引随每次 changes 更新整体重建，指针不跨更新存活。
func buildChangeByAbs(changes []git.Change) map[string]*git.Change {
	sectionRank := func(section string) int {
		switch section {
		case "unstaged":
			return 3
		case "staged":
			return 2
		default:
			return 1
		}
	}
	idx := make(map[string]*git.Change, len(changes))
	for i := range changes {
		c := &changes[i]
		abs := changeAbs(*c)
		if best, ok := idx[abs]; ok && sectionRank(c.Section) <= sectionRank(best.Section) {
			continue
		}
		idx[abs] = c
	}
	return idx
}

// changeAbs 是改动的文件绝对路径语法（RepoPath/Path，应用内统一 / 分隔）。
func changeAbs(c git.Change) string {
	return c.RepoPath + "/" + c.Path
}

// repoAt 返回 path 对应的仓库（分区派生是纯输入，经参数拿模型）。
func repoAt(repos []git.Repo, path string) *git.Repo {
	for i := range repos {
		if repos[i].Path == path {
			return &repos[i]
		}
	}
	return nil
}

// repoLabel 是仓库在下拉与提交框里的展示名。
func repoLabel(repos []git.Repo, path string) string {
	return repoDisplayName(repoAt(repos, path))
}

// repoDisplayName 的契约：根仓库（rel 为空）用名字，其余用 rel。
func repoDisplayName(repo *git.Repo) string {
	if repo == nil {
		return ""
	}
	if repo.Rel == "" {
		return repo.Name
	}
	return repo.Rel
}

// firstRepoWithChanges 挑第一个有改动的仓库，其次第一个仓库。
func firstRepoWithChanges(found []git.Repo, changes []git.Change) string {
	changed := map[string]bool{}
	for _, c := range changes {
		changed[c.RepoPath] = true
	}
	for _, repo := range found {
		if changed[repo.Path] {
			return repo.Path
		}
	}
	if len(found) > 0 {
		return found[0].Path
	}
	return ""
}

// view 是 tab 的根视图：按两态路由。
func (ws *workspace) view(c *ui.Context) {
	switch ws.mode {
	case "pick":
		ws.pickView(c)
	case "work":
		ws.workView(c)
	}
}
