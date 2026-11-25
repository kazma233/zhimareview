package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"zhimareview/internal/git"
)

// widgets 是应用内复用的界面模式。新组件先看这里有没有现成的。

// sideRow 是侧栏树（改动树与目录树）拍平后的通用渲染行。
// 两棵树共用行类型与渲染；数据源不同，拍平各自做（rebuildChangeRows / rebuildDirRows）。
type sideRow struct {
	id    string // 展开态与选中跟随的身份：改动树是 cd:/cf: 前缀 id，目录树是 abs 路径
	label string // 显示名；列表布局是仓库相对完整路径
	// abs 是行代表对象的绝对路径（复制路径/名称的目标）；空串表示该行没有
	// 行菜单（搜索结果行）
	abs string
	// change 非 nil 表示行代表一个带改动的文件：菜单提供暂存/取消暂存
	change *git.Change
	depth  int
	branch bool // 可展开行（目录/仓库节点）；文件行为 false
	// header 是分区组标题行（已暂存/未暂存）：点击折叠整组，没有右键菜单
	header bool
	open   bool
	// nameHit 是搜索结果树里「目录名含搜索词」：行文字保持主色（否则
	// 空子层的 branch 行会被当空目录弱化成灰色）
	nameHit bool
	// rank 聚合改动状态等级（rankColor 着色）；0 = 无改动，文字弱化
	rank int
	// count branch 行聚合的改动数；文件行不用
	count int
	// letter 文件行的状态字母；branch 行为空，渲染成同宽占位对齐缩进
	letter string
}

// selList 是带原生选区的列表状态对。sel 一律 -1 起步（坑 11：Go 零值 0
// 会让列表首帧凭空高亮第一行并卡死滚轮），经 newSelList 构造，不走直构。
type selList struct {
	st  ui.ListState
	sel int
}

// newSelList 构造无选中的列表状态对。
func newSelList() selList { return selList{sel: -1} }

// treeList 装配一棵侧栏树：List 原生选区（跟随条目 id，行增删后不漂移）、
// 键入 label 跳行、行自绘。返回本次点击激活的行：branch 行是瞬时开合动作，
// 选区已归 -1——已选行的重复点击不会触发 Changed，不归位就永远收不起；
// ok=false 表示本帧没有点击或选择无效。开合/打开的动作语义各树不同，
// 由调用方按 row.branch 分发。
func treeList(c *ui.Context, l *selList, key string, rows []sideRow, render func(ui.ListRow)) (sideRow, bool) {
	l.st.Selected = &l.sel
	l.st.Key = func(i int) any { return rows[i].id }
	l.st.Label = func(i int) string { return rows[i].label }
	lst := ui.List(c.Key(key), &l.st, len(rows)).Fill().Rows(render)
	if !lst.Changed() || l.sel < 0 || l.sel >= len(rows) {
		return sideRow{}, false
	}
	row := rows[l.sel]
	if row.branch || row.header {
		l.sel = -1
	}
	return row, true
}

// sideRowView 渲染侧栏树一行：缩进 + 箭头 + 字母列 + 名称 + 计数。
// 选中高亮画在行上（ListRow.Selected）；branch 行的箭头是行内独立元素，
// 点击只开合并吃掉该点击（不改变选中，Finder 披露三角语义），开合经 toggle
// 回到调用方——行点击选中由 List 处理。menu 为 nil 时不挂右键菜单。
func sideRowView(r ui.ListRow, row sideRow, toggle func(), menu func(m *ui.Menu)) {
	c := r.Context
	tk := appTokens(c)
	// 缩进是行首 spacer 而非行 padding：计数列才能固定贴右缘成对齐的一列
	// （若随行 padding 内收，深层计数会逐级左移成锯齿）；label 加 ClipX 放弃
	// min-content 地板，深层长名收缩出省略号而不是把计数挤出内容区。
	// 左 10 是列表与窗口缘的边距；右 12 避开滚动条（thumb 6px 贴视口右缘，
	// 之前计数贴右缘会被压住，实抓），统一右 padding 计数列仍对齐
	line := ui.Row(c).FillWidth().Height(tk.RowH).
		Padding(0, 12, 0, 10).Gap(4).AlignItems(ui.Center).Radius(tk.RowRadius)
	if menu != nil {
		line.ContextMenu(menu)
	}
	if r.Selected() {
		line.Background(tk.Selection)
	} else if row.header {
		// 组标题用底色与文件行区分，行高与左右边距保持一致，两个分组在
		// 视觉上连成一条列表而不是两张卡片
		line.Background(tk.Surface)
	}
	// 目录行不着状态色（改动状态看文件行的字母与文件名）；空目录/空仓库
	// 弱化显示；文件行带改动才着色
	color := tk.Text
	switch {
	case row.branch && row.count == 0 && !row.nameHit:
		color = tk.Muted
	case !row.branch && row.rank > 0:
		color = tk.rankColor(row.rank)
	}
	line.Children(func() {
		if row.header {
			// 组标题：折叠箭头 + 组名 + 改动数。箭头矢量自绘（▾/▸ 文本
			// 在 XS 字号下太小，实抓），盒子与目录图标列同宽保持对齐
			caret := ui.Box(c).Size(tk.LetterW, tk.LetterW).Shrink(0)
			caret.Draw(func(p *ui.Painter, r ui.Rect) {
				cx, cy, d := r.X+r.W/2, r.Y+r.H/2, float32(5)
				var path ui.Path
				if row.open {
					path.MoveTo(cx-d, cy-d/2).LineTo(cx, cy+d/2).LineTo(cx+d, cy-d/2)
				} else {
					path.MoveTo(cx-d/2, cy-d).LineTo(cx+d/2, cy).LineTo(cx-d/2, cy+d)
				}
				p.StrokePath(&path, 2, tk.Muted)
			})
			ui.Text(c, row.label).FontSize(tk.FontSM).FontWeight(600).TextColor(tk.Muted).
				Grow(1).SingleLine().ClipX()
			ui.Textf(c, "%d", row.count).FontSize(tk.FontXS).TextColor(tk.Muted)
			return
		}
		if row.depth > 0 {
			ui.Box(c).Width(float32(row.depth) * tk.Indent).Shrink(0)
		}
		if row.branch {
			a := ui.ButtonBase(c).Label("展开 "+row.label).Size(tk.LetterW, tk.LetterW).Children(func() {
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					drawFolderIcon(p, r, row.open, tk.Muted)
				})
			})
			if a.Clicked() {
				toggle()
			}
		}
		// 状态字母：文件行常驻；branch 行（搜索结果的内容命中文件）
		// 也有状态，排在箭头后同一字母列
		if row.letter != "" {
			ui.Text(c, row.letter).FontSize(tk.FontSM).FontWeight(600).TextColor(color).Width(tk.LetterW)
		}
		// 无字母的文件行（目录列表里未改动的文件）不放占位：名字前移到箭头列位置
		ui.Text(c, row.label).FontSize(tk.FontLG).TextColor(color).Grow(1).SingleLine().ClipX()
		if row.branch && row.count > 0 {
			ui.Textf(c, "%d", row.count).FontSize(tk.FontSM).TextColor(tk.Muted)
		}
	})
}

// drawFolderIcon 在行首的方形盒子里画文件夹开/合图标：矢量路径自绘
// （官方 Tree 的箭头同法），随窗口缩放清晰、颜色跟随令牌。合上时是单色
// 轮廓；打开时背面用淡化透明度，前盖实色，读出「掀开」的层次。
func drawFolderIcon(p *ui.Painter, r ui.Rect, open bool, c ui.Color) {
	pad := float32(1.5)
	x0, x1 := r.X+pad, r.X+r.W-pad
	y0, y1 := r.Y+pad+0.5, r.Y+r.H-pad-0.5
	rad := float32(1.5)
	tabW := (x1 - x0) * 0.45
	tabStep := (y1 - y0) * 0.26

	// 合上的文件夹轮廓：左上小 tab 折进 body 顶边，四角圆弧
	folder := func(color ui.Color) *ui.Path {
		var path ui.Path
		path.MoveTo(x0+rad, y0).
			LineTo(x0+tabW-rad, y0).
			QuadTo(x0+tabW, y0, x0+tabW, y0+tabStep).
			LineTo(x1-rad, y0+tabStep).
			QuadTo(x1, y0+tabStep, x1, y0+tabStep+rad).
			LineTo(x1, y1-rad).
			QuadTo(x1, y1, x1-rad, y1).
			LineTo(x0+rad, y1).
			QuadTo(x0, y1, x0, y1-rad).
			LineTo(x0, y0+rad).
			QuadTo(x0, y0, x0+rad, y0).
			Close()
		p.FillPath(&path, color)
		return nil
	}
	if !open {
		folder(c)
		return
	}
	// 打开：背面同轮廓淡化，前盖实色、右下角斜切，读出掀开的层次
	folder(c.Alpha(0.4))
	flapTop := y0 + (y1-y0)*0.44
	var flap ui.Path
	flap.MoveTo(x0, flapTop).
		LineTo(x1, flapTop).
		LineTo(x1-2, y1).
		LineTo(x0, y1).
		Close()
	p.FillPath(&flap, c)
}

// tapRow 是整行可点击的按钮行：label 为无障碍名称，build 放行内容。
// 文本放在 Children 里以获得完整布局控制（图标、Grow、多元素）。
// 用无外观的 ButtonBase：Button 的主题外观（底色/边框/内边距）会把
// 列表行变成卡片，并放大图标与文本的间距。
func tapRow(c *ui.Context, label string, build func()) ui.Element {
	row := ui.ButtonBase(c).Label(label).FillWidth()
	row.Children(build)
	return row
}

// drawSearchIcon 画放大镜（全局搜索入口）：圆环 + 柄，描边随主题色。
func drawSearchIcon(p *ui.Painter, r ui.Rect, color ui.Color) {
	pad := float32(3)
	lens := r.W - pad*2
	cx, cy := r.X+pad+lens*0.42, r.Y+pad+lens*0.42
	var ring ui.Path
	ring.Circle(cx, cy, lens*0.36)
	p.StrokePath(&ring, 1.5, color)
	var handle ui.Path
	handle.MoveTo(cx+lens*0.26, cy+lens*0.26).LineTo(r.X+r.W-pad, r.Y+r.H-pad)
	p.StrokePath(&handle, 1.5, color)
}

// drawCaretDown 画实心下指小三角（split button 的下拉 caret）。
func drawCaretDown(p *ui.Painter, r ui.Rect, color ui.Color) {
	pad := float32(1)
	cx := r.X + r.W/2
	var path ui.Path
	path.MoveTo(r.X+pad, r.Y+pad).
		LineTo(r.X+r.W-pad, r.Y+pad).
		LineTo(cx, r.Y+r.H-pad).
		Close()
	p.FillPath(&path, color)
}

// badge 是小号灰底圆角徽标（分区名、分支名、引用标签）。SingleLine+ClipX
// 让它在受限容器里收缩出省略号，而不是用 min-content 把兄弟挤出去。
func badge(c *ui.Context, text string) ui.Element {
	tk := appTokens(c)
	return ui.Text(c, text).FontSize(tk.FontXS).TextColor(tk.Muted).
		Background(tk.Surface).Padding(2, 6).Radius(tk.BadgeRadius).
		SingleLine().ClipX()
}

// dirShowDiffCheckbox 是主区内容头部的「查看 diff」开关：openFile 按它
// 分流，翻转时已打开的内容跟随切换（retargetOpened）。默认不勾选——
// 进目录先看原内容；只在开关管辖的内容上显示（带改动的文件、目录入口
// 的 diff），调用方负责守卫。偏好是全局的：显式翻转即落盘，所有 tab 共用。
func (ws *workspace) dirShowDiffCheckbox(c *ui.Context) {
	prefs := ws.diffPrefs()
	prev := prefs.ShowDiff
	ui.Checkbox(c, &prefs.ShowDiff, "查看 diff")
	if prefs.ShowDiff == prev {
		return
	}
	ws.app.saveConfig()
	ws.retargetOpened()
}

// mdPreviewSwitch 是 Markdown 渲染模式的开关（预览 / 源码或差异）：
// 用开关（Segmented）而不是改文案的按钮——按钮文案随状态跳变会让工具栏
// 宽度与按钮位置都在动。开关只表达两个稳定选项，不含进行中状态。
// 下标与标签顺序绑定：index 0 是 preview，1 是 other，所以 mdPreview 为真
// 对应下标 0。
func (ws *workspace) mdPreviewSwitch(c *ui.Context, preview, other string) {
	mode := 1
	if ws.mdPreview {
		mode = 0
	}
	ui.Segmented(c.Key("md-preview"), &mode, preview, other)
	ws.mdPreview = mode == 0
}

// layoutSwitch 是统一/分栏的分段切换控件：主 diff 与历史详情共用同一个
// 全局布局偏好，调用方负责在显式切换后落盘。
func layoutSwitch(c *ui.Context, seg *int) {
	ui.Segmented(c, seg, "统一", "分栏").Label("diff 布局")
}

// diffLayoutSwitch 是统一/分栏的全局偏好切换：主 diff 与历史详情都用它，
// 显式切换即写内存并落盘，所有视图/所有 tab 随即生效。
func (ws *workspace) diffLayoutSwitch(c *ui.Context) {
	prefs := ws.diffPrefs()
	prev := prefs.Layout
	layoutSwitch(c, &prefs.Layout)
	if prefs.Layout != prev {
		ws.app.saveConfig()
	}
}

// markdownPreview 是 Markdown 预览的统一容器（diff 预览与文件查看共用）：
// 内边距给正文留白，贴边滚动的长文没法读。
func markdownPreview(c *ui.Context, src string, opts *markdown.Options) {
	ui.Scroll(c).Fill().Padding(16, 24).Children(func() {
		markdown.View(c, src, opts)
	})
}

// emptyState 是居中的空态提示：一行主文案 + 一行可选副文案。
func emptyState(c *ui.Context, title, hint string) ui.Element {
	tk := appTokens(c)
	return ui.Column(c).Fill().Center().Gap(4).Children(func() {
		ui.Text(c, title).TextColor(tk.Muted).FontSize(tk.FontMD)
		if hint != "" {
			ui.Text(c, hint).TextColor(tk.Muted).FontSize(tk.FontSM)
		}
	})
}

// layoutToggle 是 diff 列表布局的垂直拨杆（on/off，轨道竖放），嵌在侧栏
// 「diff列表」段内：off=层级树（默认），on=平铺列表。enabled=false（当前
// 在「目录列表」段，布局拨杆无意义）时置灰禁用。拨杆是行内独立可点元素，
// 吃掉自己的点击，不触发所在段的段选择（同侧栏行披露三角的拦截语义）；
// 框架 Segmented 的段是内部构建的，段内放不了自定义内容，所在分段组由
// ToggleGroup 自绘（见 sidebarView）。
func layoutToggle(c *ui.Context, layout *bool, enabled bool) {
	tk := appTokens(c)
	track, dot := tk.Text.Alpha(0.16), tk.Background
	if *layout {
		track = tk.Accent
	}
	if !enabled {
		track, dot = tk.Text.Alpha(0.07), tk.Text.Alpha(0.3)
	}
	sw := ui.ButtonBase(c.Key("layout-toggle")).
		Label("切换列表布局").
		Size(13, 20).Radius(6.5).Shrink(0).Disabled(!enabled)
	if sw.Hovered() && enabled && !*layout {
		track = tk.Text.Alpha(0.26)
	}
	sw.Background(track)
	// 内容必须在 Children 闭包内创建（挂载契约，见 docs 坑记录）
	sw.Children(func() {
		// 轨道 13 宽、滑块 8 宽：左右各 2.5px 居中；on 滑块在上、off 在下。
		// 滑块带轻阴影浮起——拨杆的层次全靠它，纯平白点在浅轨上看不出是
		// 滑块（实抓）
		knob := ui.Box(c).Size(8, 8).Radius(4).Shrink(0).Background(dot).
			Shadow(0, 1, 1, 0, tk.Text.Alpha(0.18))
		if *layout {
			knob.Absolute().Left(2.5).Top(3)
		} else {
			knob.Absolute().Left(2.5).Bottom(3)
		}
	})
	if sw.Clicked() {
		*layout = !*layout
	}
}
