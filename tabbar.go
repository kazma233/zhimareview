package main

import (
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// tabBarH 是 tab 条的兜底高度：仅在全屏等 TitleBar 零值（红绿灯隐藏）
// 场景生效；正常态行高跟 bar.Height 走以对齐红绿灯（见 tabBar）。
const tabBarH float32 = 28

// tabGap 是 tab 与系统组件（红绿灯/Windows 窗口按钮）之间、以及窗口
// 右缘的固定间隙：bar.Left/Right 只给出系统组件的占位边界，紧贴边界
// 观感太挤（实抓），再垫一道。
const tabGap float32 = 12

// tabBar 是自绘标题栏：TitleBarHidden 下红绿灯浮在窗口左上角，整行
// DragWindow（拖动移动窗口、双击最大化）；左侧留白避开红绿灯
// （bar.Left，全屏时为 0），随后是 tab 列表与 ＋ 按钮，其余空白都是
// 拖动区。tab 与按钮是行内独立可点元素，各自吃掉点击（同侧栏箭头的
// 拦截语义），不触发窗口拖动。
func tabBar(c *ui.Context, a *application) {
	tk := appTokens(c)
	bar := c.TitleBar()
	// 行高跟 bar.Height：它就是「红绿灯垂直居中于其中的带高」（darwin
	// 按灯高 + 上下 inset 合成），tab 在行内 AlignItems 居中即与灯水平
	// 对齐；垫高反而把 tab 压到灯的下方（实抓：38 行里 tab 低 ~5px）。
	// 零值（全屏，灯隐藏）才落兜底，左右只留常规内边距。
	h := bar.Height
	if h < tabBarH {
		h = tabBarH
	}
	// 一次 Padding 定形：PaddingX 会整体覆盖水平值，系统组件留白就丢了
	left, right := bar.Left+tabGap, bar.Right+tabGap
	if left < tabGap {
		left = tabGap
	}
	if right < tabGap {
		right = tabGap
	}
	ui.Row(c.Key("tab-bar")).FillWidth().Height(h).
		Padding(0, right, 0, left).
		AlignItems(ui.Center).Gap(6).
		Background(tk.Surface).DragWindow().
		Children(func() {
			// tab 列表在横向滚动区里：超出窗口宽度时触摸板横滑/滚轮/箭头滚动，
			// 偏移由 TrackScroll 记在 app.tabScroll 跨帧保持；滚动区 Grow 占
			// ＋ 之前的全部剩余空间，控制组固定在右端不随内容滚走。
			// 滚动区内的 tab 各自吃掉点击，空白处仍是行的拖动区。
			// Scrollbars(false) 隐藏滚动条指示（悬停也不出），滚动能力不变：
			// 标题栏区出现滚动条观感很怪，滚动作为超宽兜底存在即可。
			ui.ScrollHorizontal(c.Key("tab-scroll")).TrackScroll(&a.tabScroll).
				Scrollbars(false).
				Grow(1).Height(h).AlignItems(ui.Center).Children(func() {
				tabs := ui.TabsBase(c.Key("tab-tabs"), &a.active, len(a.tabs))
				// tab 选中与键盘（←→/Home/End）由 TabsBase 写回 a.active，
				// 变化后统一走钩子同步标题与会话
				tabs.List.Gap(4).Children(func() {
					for i := range a.tabs {
						tabView(c, a, tabs, i, h)
					}
				})
				if tabs.List.Changed() {
					a.tabChanged()
				}
			})
			// 控制组靠右聚拢：左右滚动箭头成对相邻，＋ 同尺寸收尾
			tabArrow(c, a, -1)
			tabArrow(c, a, +1)
			// ＋ 与箭头同尺寸（14×18）。十字用矢量自绘不用字体「＋」字符：
			// 字形在字体 em-box 里有天生偏移，怎么摆都有半像素差（实抓），
			// 画出来的几何十字绝对居中
			plus := ui.ButtonBase(c).Label("新建标签页").Size(14, 18).Radius(tk.RowRadius).Shrink(0)
			plus.Children(func() {
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					cx, cy := r.X+r.W/2, r.Y+r.H/2
					arm := r.W * 0.32
					var path ui.Path
					path.MoveTo(cx-arm, cy).LineTo(cx+arm, cy)
					path.MoveTo(cx, cy-arm).LineTo(cx, cy+arm)
					p.StrokePath(&path, 1.5, tk.Muted)
				})
			})
			if plus.Clicked() {
				a.queueNewTab()
			}
		})
	// tab 条整体渲染完再应用本帧入队的增删：构建中途改 a.tabs 会让同帧
	// 的 range 循环与 TabsBase 的 n 越界（实抓 panic，见 flushTabOps）
	a.flushTabOps()
}

// tabScrollStep 是箭头单击的滚动步长：不用视口比例（应用层拿不到几何），
// 固定步长跨窗口尺寸行为一致。
const tabScrollStep float32 = 200

// tabRenameBlurDelay 是失焦提交重命名的延迟：一次点击的按下与释放之间
// 隔着一次事件循环，立即收窄 tab 会让释放点落到移走的 tab 外，导致切换
// tab 的首击被吞。延迟只影响提交时机，不影响输入与 Esc/Enter。
const tabRenameBlurDelay = 80 * time.Millisecond

// tabArrow 渲染单个实心三角滚动箭头（dir -1 左 / +1 右）。恒在：没溢出
// （MaxX 是上一帧布局写入的滚动余量）或已滚到对应尽头时置灰禁用。
// 点击平滑滚一个步长（Spring）。
func tabArrow(c *ui.Context, a *application, dir int) {
	tk := appTokens(c)
	atEnd := a.tabScroll.MaxX <= 0 ||
		dir < 0 && a.tabScroll.X <= 0 ||
		dir > 0 && a.tabScroll.X >= a.tabScroll.MaxX
	label := "向左滚动标签页"
	if dir > 0 {
		label = "向右滚动标签页"
	}
	// 宽比高窄（14×18）：三角天然窄高比，缩宽省横向空间
	btn := ui.ButtonBase(c).Label(label).Size(14, 18).Radius(tk.RowRadius).Shrink(0).Disabled(atEnd)
	bg := tk.Text.Alpha(0.12)
	if btn.Hovered() {
		bg = tk.Text.Alpha(0.2)
	}
	btn.Background(bg)
	// 内容必须在 Children 闭包内创建（挂载契约，见 docs 坑记录）
	btn.Children(func() {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			drawSolidArrow(p, r, dir > 0, tk.Text)
		})
	})
	if btn.Clicked() {
		a.tabScroll.ScrollTo(a.tabScroll.X+float32(dir)*tabScrollStep, 0, ui.Smooth)
	}
}

// drawSolidArrow 画实心三角箭头（右指/左指），矢量路径随窗口缩放清晰。
func drawSolidArrow(p *ui.Painter, r ui.Rect, right bool, color ui.Color) {
	pad := float32(4)
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	back := cx - (r.W/2 - pad)
	tip := cx + (r.W/2 - pad)
	var path ui.Path
	if right {
		path.MoveTo(back, cy-(r.H/2-pad)).LineTo(tip, cy).LineTo(back, cy+(r.H/2-pad)).Close()
	} else {
		path.MoveTo(tip, cy-(r.H/2-pad)).LineTo(back, cy).LineTo(tip, cy+(r.H/2-pad)).Close()
	}
	p.FillPath(&path, color)
}

// tabColorMenu 是右键「颜色」子菜单的顺序与文案；颜色本身由 tokens 映射。
var tabColorMenu = []struct{ key, label string }{
	{tabColorRed, "红"},
	{tabColorOrange, "橙"},
	{tabColorGreen, "绿"},
	{tabColorBlue, "蓝"},
	{tabColorPurple, "紫"},
	{tabColorPink, "粉"},
	{tabColorGray, "灰"},
}

// tabView 渲染单个 tab：项目名 + 关闭钮。每个 tab 都有独立胶囊底做分割：
// 激活态高对比底 + 字重 600，非激活态 subtle 底（hover 加深）；标签 ClipX
// 放弃 min-content 地板，tab 多时收缩出省略号而不是把 ＋ 挤出条外。
// × 是行内独立按钮，吃掉该点击不触发 tab 选择。tab 也是拖拽源与放置目标，
// 右键菜单提供重命名与颜色；重命名时标题位置换成输入框。
func tabView(c *ui.Context, a *application, tabs ui.TabsParts, i int, barH float32) {
	tk := appTokens(c)
	ws := a.tabs[i]
	tab := tabs.Tab(i).Height(barH - 6).Radius(tk.RowRadius).
		PaddingX(8).Gap(4).AlignItems(ui.Center).ClipX()
	// 失焦提交延迟一拍（tabRenameBlurDelay）：按下别的控件时，释放事件
	// 要等下一次事件循环；立即收窄 tab 会让释放点落到移走的 tab 外，
	// 首次点击被吞（实测）。焦点回来则取消挂起的提交。
	if ws.tabMeta.blurPending {
		switch {
		case tab.FocusWithin():
			ws.tabMeta.blurPending = false
		case !c.Now().Before(ws.tabMeta.blurAt):
			ws.finishTabRename(true)
		}
	}
	name := ws.tabTitle()
	editing := ws.tabMeta.renaming
	tab.Label("标签页 " + name)
	tab.Tooltip(ws.tabTooltip())
	if i != a.active {
		// 非激活：subtle 底把相邻 tab 视觉分开，hover 加深给可选反馈
		bg := tk.Text.Alpha(0.05)
		if tab.Hovered() {
			bg = tk.Text.Alpha(0.1)
		}
		tab.Background(bg)
	} else {
		tab.Background(tk.Background)
	}
	// 重命名中不挂拖拽源：输入框内的按压属于文本编辑，不能被拖拽手势吃掉
	if !editing {
		tab.Drag(i)
	}
	dragSrc, dragOver := ui.DragOver[int](tab)
	color, hasColor := tk.tabColor(ws.tabMeta.color)
	// 底部色条与拖拽插入线都画在内容之上：色条不占文字宽度，插入线
	// 指示松手后源 tab 会落在目标的哪一侧（向右拖画右缘，向左拖画左缘）。
	if hasColor || (dragOver && dragSrc != i) {
		tab.DrawOver(func(p *ui.Painter, r ui.Rect) {
			if hasColor && r.W > 20 {
				h := float32(2)
				p.Fill(ui.Rect{X: r.X + 6, Y: r.Y + r.H - h - 1, W: r.W - 12, H: h}, color, h/2)
			}
			if dragOver && dragSrc != i {
				x := r.X
				if dragSrc < i {
					x = r.X + r.W - 2
				}
				p.Fill(ui.Rect{X: x, Y: r.Y + 3, W: 2, H: r.H - 6}, tk.Accent, 1)
			}
		})
	}
	// 内容必须在 Children 闭包内创建：节点在创建时挂到当前父容器，
	// 闭包外建的元素不会进 tab（挂载契约，见 docs 坑记录）
	tab.Children(func() {
		if editing {
			inp := ui.TextInputBase(c.Key(ws), &ws.tabMeta.draft).
				Width(ws.renameInputWidth()).Height(barH-12).Padding(0, 4).Radius(3).
				FontSize(tk.FontSM).Background(tk.Background).Border(1, tk.Border).Shrink(0)
			if ws.tabMeta.renameFocus {
				ws.tabMeta.renameFocus = false
				inp.Focus()
				inp.SetTextSelection(0, utf8.RuneCountInString(ws.tabMeta.draft))
			}
			switch {
			case inp.Submitted():
				ws.finishTabRename(true)
			case inp.Shortcut(0, ui.KeyEscape):
				ws.finishTabRename(false)
			case inp.FocusLeft():
				ws.tabMeta.blurPending = true
				ws.tabMeta.blurAt = c.Now().Add(tabRenameBlurDelay)
				c.After(tabRenameBlurDelay)
			}
		} else {
			label := ui.Text(c, name).FontSize(tk.FontSM).SingleLine().ClipX()
			if i == a.active {
				label.FontWeight(600)
			} else {
				label.TextColor(tk.Muted)
			}
		}
		// 未读错误标记：错误弹窗只在该 tab 激活时弹，后台 tab 的错误靠
		// 这个红点提示存在；用户看过弹窗后红点消失
		if ws.err != nil && !ws.errSeen {
			ui.Box(c).Size(6, 6).Radius(3).Background(tk.Danger).Shrink(0)
		}
		x := ui.ButtonBase(c).Label("关闭标签页 "+name).Size(14, 14).Radius(3)
		x.Children(func() {
			ui.Text(c, "×").FontSize(tk.FontSM).TextColor(tk.Muted)
		})
		if x.Clicked() {
			a.queueCloseTab(i)
		}
	})
	if !editing && tab.DoubleClicked() {
		ws.startTabRename()
	}
	if from, ok := ui.Drop[int](tab); ok && from >= 0 && from < len(a.tabs) && from != i {
		a.queueMoveTab(a.tabs[from], a.tabs[i])
	}
	tab.ContextMenu(func(m *ui.Menu) {
		if m.Item("重命名").Disabled(editing).Chosen() {
			ws.startTabRename()
		}
		m.Submenu("颜色", func(sub *ui.Menu) {
			if sub.Item("无颜色").Radio(ws.tabMeta.color == "").Chosen() {
				ws.setTabColor("")
			}
			for _, opt := range tabColorMenu {
				if sub.Item(opt.label).Radio(ws.tabMeta.color == opt.key).Chosen() {
					ws.setTabColor(opt.key)
				}
			}
		})
	})
	// 只记非编辑态的宽度：编辑框按它定宽，避免输入框出现/消失时整条
	// tab 变宽变窄，后面的 tab 在点击过程中移出指针落点。
	if !editing {
		if w := tab.Bounds().W; w > 0 {
			ws.tabMeta.width = w
		}
	}
}

// renameInputWidth 让编辑框与原 tab 内容近似同宽：扣掉左右 padding、与
// 关闭钮的 gap 和关闭钮本身，未读错误点占据的 10px 也扣掉；未知宽度或
// 过窄时兜底，保证输入可用。
func (ws *workspace) renameInputWidth() float32 {
	w := ws.tabMeta.width
	if w <= 0 {
		return 140
	}
	w -= 16 + 4 + 14
	if ws.err != nil && !ws.errSeen {
		w -= 10
	}
	return min(max(w, 40), 320)
}

// startTabRename 进入内联编辑：草稿从当前展示名开始，下一帧输入框聚焦并
// 全选，用户直接输入即可替换。
func (ws *workspace) startTabRename() {
	ws.tabMeta.draft = ws.tabTitle()
	ws.tabMeta.renaming = true
	ws.tabMeta.renameFocus = true
	ws.tabMeta.blurPending = false
	ws.tabMeta.blurAt = time.Time{}
}

// finishTabRename 结束编辑：commit 时 trim 后落地（空串=回自动名，与自动
// 名相同也清空覆盖，避免会话里存冗余名字）；取消只退出编辑。
func (ws *workspace) finishTabRename(commit bool) {
	if !ws.tabMeta.renaming {
		return
	}
	ws.tabMeta.renaming = false
	ws.tabMeta.renameFocus = false
	ws.tabMeta.blurPending = false
	ws.tabMeta.blurAt = time.Time{}
	if !commit {
		return
	}
	name := strings.TrimSpace(ws.tabMeta.draft)
	if ws.mode == "work" && name == filepath.Base(ws.root) {
		name = ""
	}
	ws.tabMeta.name = name
	if ws.app != nil {
		if ws == ws.app.activeTab() {
			ws.app.syncTitle()
		}
		ws.app.saveSession()
	}
}

// setTabColor 落地右键菜单选择的颜色键；空串表示清除。颜色不影响其他
// tab 状态，下一帧重绘即可，不需要走 tab 操作队列。
func (ws *workspace) setTabColor(key string) {
	if ws.tabMeta.color == key {
		return
	}
	ws.tabMeta.color = key
	if ws.app != nil {
		ws.app.saveSession()
	}
}
