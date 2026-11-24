package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"zhimareview/internal/git"
)

// tabMeta 是 tab 条上的展示元数据：用户重命名/设色与内联编辑态。归该
// tab 的 workspace 所有，随会话按 tab 顺序持久化（name/color）。
type tabMeta struct {
	// name 空=按根目录自动命名；color 空=无颜色，否则是 theme.go 的预设键
	name  string
	color string
	// renaming 时标题位置换成输入框，draft 是编辑中的文本；
	// renameFocus 是一次性聚焦/全选标记（开始编辑后的首帧消费）
	renaming    bool
	draft       string
	renameFocus bool
	// blurPending/blurAt 把「失焦提交」推迟一拍：失焦发生在按下另一控件
	// 的那一帧，若同帧收窄 tab，释放事件会落空、点击丢给空白（实测）。
	blurPending bool
	blurAt      time.Time
	// width 是上一帧非编辑态的 tab 宽度：重命名输入框按它收窄，避免提交
	// 时 tab 收窄、后面的 tab 横向跳动（点击因此落空）。
	width float32
}

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
	mode    string
	root    string
	recent  []string
	tabMeta tabMeta
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
	// fetchAll 是顶栏「全部 fetch」的在途状态与进度模态：running 与单仓库
	// fetch/pull 互斥，避免同一仓库的并发 git 写操作；展示细节见 remote.go
	fetchAll fetchAllPane

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

// tabTitle 是 tab 条与窗口标题共用的展示名：用户重命名优先，否则 work
// 态取根目录名、pick 态是入口页标题。
func (ws *workspace) tabTitle() string {
	if ws.tabMeta.name != "" {
		return ws.tabMeta.name
	}
	if ws.mode == "work" {
		return filepath.Base(ws.root)
	}
	return "新标签页"
}

// tabTooltip 是 tab 悬停概况：全路径 + 仓库/改动数量，扫描中替代计数，
// 错误标题追加一行。生成的字符串每帧随状态派生，不做缓存。
func (ws *workspace) tabTooltip() string {
	if ws.mode != "work" {
		return "新标签页 · 未选择目录"
	}
	body := ws.root
	if ws.scanning {
		body += "\n扫描中…"
	} else {
		body += fmt.Sprintf("\n%d 个仓库 · %d 处改动", len(ws.repos), len(ws.changes))
	}
	if ws.err != nil {
		body += "\n错误：" + ws.err.title
	}
	return body
}

// workView 是工作区态：顶栏（页面切换也在这里）+ 下方整页。
// 提交历史是全宽独立页，侧栏只属于工作区页。
func (ws *workspace) workView(c *ui.Context) {
	tk := appTokens(c)
	ui.Column(c).Fill().Children(func() {
		// 顶栏精简（tab 条已标识项目）：无 logo、36px，路径/统计/页面切换/按钮保留
		ui.Row(c).FillWidth().Height(36).Padding(0, 12).Gap(10).AlignItems(ui.Center).Background(tk.Surface).Children(func() {
			// 路径与统计紧挨着放：路径不用 Grow 占中间（会把统计推到右侧
			// 空隙另一头），改用 ClipX 放弃 min-content 地板，过长时收缩截断；
			// 剩余空间垫给右侧，页面切换与按钮仍靠右
			ui.Text(c, ws.root).Font(monoFont()).FontSize(tk.FontSM).TextColor(tk.Muted).SingleLine().TruncateMiddle().ClipX()
			stats := fmt.Sprintf("%d 仓库 · %d 文件", len(ws.repos), len(ws.changes))
			if ws.scanning {
				stats = "扫描中…"
			}
			ui.Text(c, stats).FontSize(tk.FontSM).TextColor(tk.Muted)
			ui.Spacer(c)
			ui.Tabs(c.Key("main-tab"), &ws.mainTab, "工作区", "提交历史")
			// 文案不随状态改；运行中点击重新打开进度模态，不重复发起 fetch
			if ui.Button(c, "全部 fetch").
				Disabled(ws.hist.remoteBusy != "" || len(ws.repos) == 0).
				Clicked() {
				ws.fetchAllRepos()
			}
			if ui.Button(c, "刷新").Clicked() {
				ws.refresh()
			}
		})
		if ws.mainTab == 0 {
			// 占「剩余高度」必须用 Grow 而非 Fill：Fill 是父容器高度的 100%，
			// 加上顶栏后会基数超限，Split 的 min-content 又不许收缩，整页溢出
			// 窗口底部（Absolute 锚定的元素跟着出窗，List 视口高度失真）
			ui.Split(c.Key("sidebar-split"), &ws.sidebarSize, func() { ws.sidebarView(c) }, func() { ws.mainView(c) }).FillWidth().Grow(1)
		} else {
			ws.historyView(c)
		}
	})
}

// sidebarView 是侧栏：diff 列表（层级树/列表布局）或目录列表（真实文件树）。
// 顶部分段占满行宽；「diff列表」段内嵌布局拨杆（层级树/平铺列表，见
// layoutToggle）。
func (ws *workspace) sidebarView(c *ui.Context) {
	tk := appTokens(c)
	ui.Column(c).Fill().Background(tk.Background).Children(func() {
		// 顶行二选一：常态是分段组 + 右侧搜索 icon；搜索态是输入框 +
		// 恒在的退出 ×——搜索期间分段让位，范围锁定为进入时的 tab，
		// 切换不会打断结果。FillEqually（父宽 100%）必须套在 Grow 的中间
		// 容器里，否则兄弟元素被挤出行外（实抓两次）
		if ws.search.open {
			// 一体的搜索输入框：完全复刻框架 SearchField 的观感（主题同款
			// 内边距/圆角/边框/放大镜/清空钮），但清空钮无条件渲染——框架
			// 版只在有词时出现，空着就退不出搜索态（实抓）
			// 外层行边距与 Segmented 行一致（左右 8、顶 6）；行高与常态行一致
			// （38，内容 32 + 顶部 6），切换搜索态时行高不变，下方组件不跳动。
			// 框 Stretch 填满行高，水平内边距为框架 SearchField 同款
			ui.Row(c).FillWidth().Height(38).Padding(6, 8, 0, 8).AlignItems(ui.Stretch).Children(func() {
				t := c.Theme()
				ui.Row(c).FillWidth().FillHeight().
					AlignItems(ui.Center).Gap(t.Space(1.5)).Padding(0, t.Space(2.5)).
					Radius(t.Radius).Background(t.Surface).Border(1, t.Border).
					Label("全局搜索 搜索文件 / 目录，回车搜内容").
					Children(func() {
						ui.Box(c).Size(t.Space(3.75), t.Space(3.75)).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
							drawSearchIcon(p, r, t.TextMuted)
						})
						inp := ui.TextInputBase(c.Key("global-search"), &ws.search.query).
							Grow(1).MinWidth(t.Space(15))
						if ws.search.focus {
							ws.search.focus = false
							inp.Focus()
						}
						if inp.Changed() {
							ws.onSearchQueryChanged()
						}
						if inp.Submitted() {
							ws.runContentSearch()
						}
						// Esc 退出搜索是窗口级快捷键：焦点落在结果列表/内容区后
						// （输入框早失焦了）Esc 仍要能退出；焦点在输入框内时
						// 输入框自身吃掉的键不受影响
						c.OnWindowShortcut(0, ui.KeyEscape, func() { ws.closeSearch() })
						clear := ui.ButtonBase(c).Label("清空搜索词").Size(t.Space(4), t.Space(4)).
							Radius(t.Space(2)).Shrink(0).Background(t.TextMuted.Alpha(0.5))
						clear.Draw(func(p *ui.Painter, r ui.Rect) {
							k := r.W * 0.3
							var x ui.Path
							x.MoveTo(r.X+k, r.Y+k).LineTo(r.X+r.W-k, r.Y+r.H-k).
								MoveTo(r.X+r.W-k, r.Y+k).LineTo(r.X+k, r.Y+r.H-k)
							p.StrokePath(&x, 1.5, t.Background)
						})
						if clear.Clicked() {
							ws.search.query = ""
							ws.closeSearch()
						}
					})
			})
		} else {
			// 顶部 6px 呼吸空隙：分段组贴着 tab 条太挤（实抓）
			ui.Row(c).FillWidth().Height(38).Padding(6, 8, 0, 8).Gap(6).AlignItems(ui.Center).Children(func() {
				// 中间容器 Grow 占剩余宽度，分段组 FillEqually 均分。用
				// ToggleGroup 自绘两段而非框架 Segmented：框架版的段是内部
				// 构建的，「diff列表」段内放不了布局拨杆。两段 bool 每帧从
				// sidebarTab 派生（初值天然互斥），选中观感由 ToggleGroup
				// 的按下态给出，与原 Segmented 一致
				ui.Row(c).Grow(1).Children(func() {
					changes := ws.sidebarTab != "dir"
					dir := !changes
					ui.ToggleGroup(c.Key("sidebar-mode"), func() {
						ui.Toggle(c.Key("mode-changes"), &changes, "").FillEqually().Children(func() {
							ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "diff列表")
								// 目录列表段激活时布局拨杆无意义：置灰禁用
								layoutToggle(c, &ws.tree.layout, changes)
							})
						})
						ui.Toggle(c.Key("mode-dir"), &dir, "目录列表").FillEqually()
					}).FillEqually()
					if changes && ws.sidebarTab != "changes" {
						ws.sidebarTab = "changes"
					} else if dir && ws.sidebarTab != "dir" {
						ws.sidebarTab = "dir"
						if ws.dirs.cache[ws.root] == nil {
							ws.loadDir(ws.root)
						}
					}
				})
				// 搜索入口：放大镜 icon，点击进入搜索态
				icon := ui.ButtonBase(c).Key("open-search").Size(24, 24).Radius(tk.RowRadius).
					Label("搜索文件与内容").Shrink(0)
				if icon.Hovered() {
					icon.Background(tk.SurfaceHover)
				}
				icon.Draw(func(p *ui.Painter, r ui.Rect) {
					drawSearchIcon(p, r, tk.Muted)
				})
				if icon.Clicked() {
					ws.openSearch()
				}
			})
		}
		// 内容容器是浮动按钮的定位父级：Absolute 相对它的 padding box 贴右下角。
		// 占「剩余高度」用 Grow 而非 Fill（Fill=父容器 100%，叠加分段行后基数
		// 超限、min-content 不许收缩，列底越出窗口——List 视口高度失真到无
		// 滚动余量）
		ui.Column(c).FillWidth().Grow(1).Children(func() {
			// 搜索接管内容区：结果列表替代 diff 列表 / 目录列表
			if ws.searching() {
				ws.renderSearchResults(c)
			} else {
				ws.renderSidebarContent(c)
			}
		})
	})
}

// renderSidebarContent 按侧栏模式渲染主体。两个 tab、三种布局（目录树 /
// 改动层级树 / 改动列表）都是同一模式：拍平行 + 原生 List（treeList +
// sideRowView），差别只在数据适配与选中动作。
func (ws *workspace) renderSidebarContent(c *ui.Context) {
	if ws.sidebarTab == "dir" {
		switch {
		case len(ws.dirs.rows) == 0 && !ws.scanning:
			emptyState(c, "加载中…", "")
		default:
			rows := ws.dirs.rows
			if row, ok := treeList(c, &ws.dirs.list, "dir-tree", rows, func(r ui.ListRow) {
				sideRowView(r, rows[r.Index], func() { ws.toggleDir(rows[r.Index].id) })
			}); ok {
				if row.branch {
					ws.toggleDir(row.id)
				} else {
					ws.openFile(row.id)
				}
			}
		}
		return
	}

	switch {
	case ws.scanning && len(ws.repos) == 0:
		emptyState(c, "扫描中…", "")
	case len(ws.repos) == 0:
		emptyState(c, "没有找到 git 仓库", "")
	case len(ws.changes) == 0:
		emptyState(c, "无改动", "")
	case ws.tree.layout:
		rows := ws.tree.flatRows
		if row, ok := treeList(c, &ws.tree.flatList, "flat-list", rows, func(r ui.ListRow) {
			sideRowView(r, rows[r.Index], func() {})
		}); ok {
			// 列表布局全是叶子行；行 id 与树文件行同 id（fileID），
			// 经 tree.index 取改动，不依赖 flat 下标与行集对齐
			if item := ws.tree.index[row.id]; item != nil && item.change != nil {
				ws.openDiff(*item.change)
			}
		}
	default:
		rows := ws.tree.rows
		if row, ok := treeList(c, &ws.tree.list, "changes-tree", rows, func(r ui.ListRow) {
			sideRowView(r, rows[r.Index], func() { ws.toggleChange(rows[r.Index].id) })
		}); ok {
			if row.branch {
				ws.toggleChange(row.id)
			} else if item := ws.tree.index[row.id]; item != nil && item.change != nil {
				ws.openDiff(*item.change)
			}
		}
	}
}

// mainView 是工作区页的主区：按打开内容路由（页面切换在顶栏）。
func (ws *workspace) mainView(c *ui.Context) {
	switch {
	case ws.diffv.open != nil:
		ws.diffView(c)
	case ws.dirs.file != nil:
		ws.fileMainView(c)
	default:
		ws.landingView(c)
	}
}

// landingView 是未打开 diff/文件时的主区：空态提示。
// 不做提交功能，这里没有提交框。
func (ws *workspace) landingView(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(4).Children(func() {
		switch {
		case len(ws.repos) == 0 && !ws.scanning:
			emptyState(c, "没有找到 git 仓库", "换一个目录重新扫描")
		case len(ws.changes) == 0 && !ws.scanning:
			emptyState(c, "工作区干净", "所有仓库都没有未提交的改动")
		default:
			emptyState(c, "点击左侧文件查看", "")
		}
	})
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
