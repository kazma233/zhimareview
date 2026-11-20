package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"zhimareview/internal/git"
)

// diffSides 是一个改动文件的两侧文本；binary 时内容为空（binData 带工作区当前版本字节供图片预览）。
type diffSides struct {
	oldValue string
	newValue string
	binary   bool
	binData  []byte
}

// loadSides 读取一个改动的两侧文本：
// unstaged 用 index 对工作区，staged 用 HEAD 对 index，untracked 直接看工作区。
// 二进制时读取工作区当前版本字节，供图片预览。
func loadSides(v git.Change) *diffSides {
	s := &diffSides{}
	fillBinary := func() {
		s.binary = true
		// 图片扩展名时带工作区当前版本字节，diff 视图可预览
		if isImagePath(v.Path) {
			if data, err := os.ReadFile(changeAbs(v)); err == nil {
				s.binData = data
			}
		}
	}
	switch v.Section {
	case "untracked":
		content, found, err := git.ReadWorktreeFile(v.RepoPath, v.Path)
		if err != nil || !found {
			return s
		}
		// 未跟踪文件没有 diff 可问，直接看内容里有没有 NUL
		if isBinary(content) {
			fillBinary()
			return s
		}
		s.newValue = content
		return s
	case "staged":
		var err error
		if s.binary, err = git.IsBinaryDiff(v.RepoPath, v.Path, true); err != nil {
			s.binary = false
		}
		if s.binary {
			fillBinary()
			return s
		}
		// 新文件暂存后 HEAD 没有该对象，旧侧为空
		s.oldValue, _, _ = git.ShowObject(v.RepoPath, "HEAD", v.Path)
		s.newValue, _, _ = git.ShowObject(v.RepoPath, ":0", v.Path)
		return s
	default:
		var err error
		if s.binary, err = git.IsBinaryDiff(v.RepoPath, v.Path, false); err != nil {
			s.binary = false
		}
		if s.binary {
			fillBinary()
			return s
		}
		s.oldValue, _, _ = git.ShowObject(v.RepoPath, ":0", v.Path)
		content, found, err := git.ReadWorktreeFile(v.RepoPath, v.Path)
		if err == nil && found {
			s.newValue = content
		}
		return s
	}
}

// isBinary 前 8KB 出现 NUL 即按二进制处理，不强行当文本显示。
func isBinary(content string) bool {
	head := content
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte([]byte(head), 0) >= 0
}

// diffPane 是主区 diff 视图的状态，也是「打开会话」的所有者：每次打开
// 递增 seq（世代），在途加载凭世代落地（最新打开者胜），翻转（flip）是
// 同一会话内的原地改写、世代不变。唯一写点：openDiffAs/applyDiffState
// 的打开与加载、toggleStageFile 的分区翻转、closeDiff/reloadDiff、
// 渲染帧里的布局与展开开关。
type diffPane struct {
	open *git.Change // 当前打开的 diff（nil=未打开）
	// seq 是打开会话世代：openDiffAs 递增并捕获，applyDiffState 校验
	// 相等才落地。替代旧的结构体值相等判定——不依赖 git.Change 可比较
	//（加非比较字段即编译炸），且 Code/RepoRel 等展示字段不参与同一性
	seq     int
	loading bool
	sides   *diffSides
	bitmap  *ui.Bitmap // 二进制图片的解码结果
	// expand 是当前打开 diff 的初始展开态：目录列表打开=true（改动全部
	// 可见），diff 列表打开=false（上下文折叠）；头部「展开未修改」
	// checkbox 以它翻转
	expand bool
	// fromDir 标记是否目录列表入口打开：「查看 diff」开关翻转时只切换
	// 目录入口的内容，diff 列表打开的 diff 不受该开关管辖
	fromDir bool
	// jumpLine 是新侧要定位到的行（1 起，0 = 无）：搜索结果的内容命中
	// 打开 diff 时带上，布局量好后（下一帧）滚动一次并清零
	jumpLine int
	// ed 是官方 diff 视图的跨帧状态（缓冲、布局、展开的上下文与滚动位置），
	// 查找条与还原按钮也由它承载；布局统一/分栏的偏好在窗口级 ws.diffSeg，
	// 渲染帧经 diffLayout 换算后写入 ed.Layout
	ed editor.DiffState
}

// target 返回当前打开 diff 的文件绝对路径；未打开返回空串（调用方必须
// 先判 open——空串在 hitHint 语义里是全命中）。
func (d *diffPane) target() string {
	if d.open == nil {
		return ""
	}
	return changeAbs(*d.open)
}

// flip 在当前会话内翻转打开 diff 的分区（暂存/取消暂存成功后）：原地
// 改写、世代不变，reloadDiff 按同一会话重读。
func (d *diffPane) flip() {
	if d.open == nil {
		return
	}
	if d.open.Section == "staged" {
		d.open.Section = "unstaged"
	} else {
		d.open.Section = "staged"
	}
}

// openDiff 打开一个文件的 diff（diff 列表入口：上下文默认折叠）：先置占位
// 再后台加载两侧。帧内调用时改状态即触发重建，无需 Invalidate。
func (ws *workspace) openDiff(v git.Change) {
	ws.diffv.fromDir = false
	ws.openDiffAs(v, false, 0)
}

// openDiffExpanded 目录列表入口：文件带改动时直接展示 diff，默认全展开。
func (ws *workspace) openDiffExpanded(v git.Change) {
	ws.openDiffExpandedAt(v, 0)
}

// openDiffExpandedAt 是 openDiffExpanded 的带定位版本：打开后滚到新侧第
// line 行（1 起，0 = 从头），搜索结果的内容命中靠它定位。
func (ws *workspace) openDiffExpandedAt(v git.Change, line int) {
	ws.diffv.fromDir = true
	ws.openDiffAs(v, true, line)
}

func (ws *workspace) openDiffAs(v git.Change, expanded bool, line int) {
	ws.dirs.file = nil // 主区以最后点击的为准：打开 diff 即让位出文件查看
	// 预览模式不记忆：每次打开都是差异视图，用户手动切预览
	ws.mdPreview = false
	ws.diffv.open = &v
	ws.diffv.seq++
	seq := ws.diffv.seq
	ws.diffv.expand = expanded
	ws.diffv.loading = true
	ws.diffv.sides = nil
	ws.diffv.jumpLine = line

	loadAsync(ws, func() *diffSides { return loadSides(v) },
		func(sides *diffSides) { ws.applyDiffState(seq, v, sides) })
}

func (ws *workspace) applyDiffState(seq int, v git.Change, sides *diffSides) {
	// 只接受仍是当前打开会话的结果（最新打开者胜；翻转不改世代）
	if ws.diffv.open == nil || ws.diffv.seq != seq {
		return
	}
	ws.diffv.sides = sides
	ws.diffv.loading = false
	ws.diffv.bitmap = nil
	if ws.diffv.jumpLine > 0 {
		// 定位等布局量好行高后的下一帧：请求重绘，别让跳转停在悬空状态
		ws.app.invalidate()
	}
	// Markdown 预览的本地图片按文件所在目录解析
	ws.mdOpts.LoadImage = markdown.FileImages(dirOf(changeAbs(v)))
	if sides.binary {
		// 图片在状态应用时解码一次（帧内不做 IO/解码）
		if len(sides.binData) > 0 {
			if bmp, err := ui.DecodeBitmap(sides.binData); err == nil {
				ws.diffv.bitmap = bmp
			}
		}
		return
	}
	// 官方 diff 视图：统一/分栏、折叠上下文、内置查找；staged 只读无还原。
	// ExpandUnchanged 按入口给定：目录列表打开=全展开，diff 列表=折叠
	// （布局在渲染帧按 ws.diffSeg 写入，打开时不定）
	ws.diffv.ed.Old = editor.NewBuffer(sides.oldValue)
	ws.diffv.ed.New = editor.NewBuffer(sides.newValue)
	lang := editor.LanguageFor(v.Path) // 未知扩展名返回 nil，按纯文本
	ws.diffv.ed.Old.SetLanguage(lang)
	ws.diffv.ed.New.SetLanguage(lang)
	ws.diffv.ed.ExpandUnchanged = ws.diffv.expand
	ws.diffv.ed.HunkActions = v.Section != "staged"
	ws.diffv.ed.RenderHunkActions = ws.renderHunkActions
	// 折叠分隔条走应用版渲染：官方默认的「N unmodified lines」按钮一次
	// 点击全展开整段折叠行，这里改为分步展开（见 renderSeparator）
	ws.diffv.ed.RenderSeparator = ws.renderSeparator
}

// diffLayout 把分段选择换算成官方布局：0=统一，1=分栏。
func diffLayout(seg int) editor.DiffLayout {
	if seg == 1 {
		return editor.DiffSplit
	}
	return editor.DiffUnified
}

// renderSeparator 官方分隔条的分步展开版：官方 DefaultSeparator 的「N
// unmodified lines」按钮点击是 ExpandAll（整段折叠行一次全展开），这里改
// 为每次点击至多展开 ExpansionLines（默认 100）行，连续点击逐段推进；
// Shift+点击与两侧尖角按钮的行为不变，仍可全展开，头部「展开未修改」
// 开关也不受影响。外观与结构沿用官方样式。hunk 头分支只在 SetPatch 构建
// diff 时出现，主 diff 视图用两侧缓冲构建不会触发；SetPatch 的历史视图
// 未挂本渲染器，仍走官方默认。
func (ws *workspace) renderSeparator(c *ui.Context, sep ui.Element, s *editor.DiffSeparator) {
	t := c.Theme()
	ct := s.Theme()
	const pad = 4
	sep.Padding(pad)
	sep.Children(func() {
		// 官方底色是内部 Lab 混色（背景 94% + 文本 6%），这里用文本色
		// 6% 透明度叠出同等观感
		bar := ui.Row(c).Grow(1).AlignSelf(ui.Stretch).AlignItems(ui.Center).Gap(t.Space(0.5)).Padding(0, t.Space(1.5)).
			Radius(6).Background(ct.Text.Alpha(0.06))
		bar.Children(func() {
			if s.Lines == 0 {
				ui.Text(c, s.Header).Font(s.Font().Family).FontSize(s.Font().Size - 1).TextColor(t.TextMuted).SingleLine().Shrink(1)
				return
			}
			expand := func(fn func()) {
				if c.Modifiers()&ui.Shift != 0 {
					fn = s.ExpandAll
				}
				fn()
			}
			chevron := func(dir int, label string, fn func()) {
				b := ui.ButtonBase(c).Key(label).Size(20, 20).Radius(4).Label(label).Tooltip(label).Cursor(ui.CursorPointer)
				if b.Hovered() {
					b.Background(ct.Text.Alpha(0.08))
				}
				b.Draw(func(p *ui.Painter, r ui.Rect) {
					cx, cy, d := r.X+r.W/2, r.Y+r.H/2, float32(3.5)
					var path ui.Path
					switch dir {
					case 1:
						path.MoveTo(cx-d, cy-d/2).LineTo(cx, cy+d/2).LineTo(cx+d, cy-d/2)
					case -1:
						path.MoveTo(cx-d, cy+d/2).LineTo(cx, cy-d/2).LineTo(cx+d, cy+d/2)
					default:
						path.MoveTo(cx-d, cy-2).LineTo(cx, cy-2-d).LineTo(cx+d, cy-2)
						path.MoveTo(cx-d, cy+2).LineTo(cx, cy+2+d).LineTo(cx+d, cy+2)
					}
					p.StrokePath(&path, 1.5, t.TextMuted)
				})
				if b.Clicked() {
					expand(fn)
				}
			}
			switch {
			case s.First:
				chevron(-1, "Expand up", s.ExpandUp)
			case s.Last:
				chevron(1, "Expand down", s.ExpandDown)
			case s.Lines <= s.Step:
				chevron(0, "Expand all", s.ExpandAll)
			default:
				chevron(1, "Expand down", s.ExpandDown)
				chevron(-1, "Expand up", s.ExpandUp)
			}
			label := fmt.Sprintf("%d unmodified lines", s.Lines)
			b := ui.ButtonBase(c).Key("expand-all").Padding(t.Space(0.5), t.Space(1)).Radius(4).Label("Expand " + label).Cursor(ui.CursorPointer)
			b.Children(func() { ui.Text(c, label).FontSize(max(t.FontSize-1, 11)).TextColor(t.TextMuted) })
			if b.Clicked() {
				// 标签按钮的分步方向沿各段可用的展开方向：首段之上只有
				// 向上（朝第一处改动），其余段向下（与首个尖角一致）
				step := s.ExpandDown
				if s.First {
					step = s.ExpandUp
				}
				expand(step)
			}
		})
	})
}

// expandToggle 是 diff 头部的展开/收起图标开关：收起态画上下外扩双尖角
// （点击全展开），展开态上下尖角相向（点击收起全部），方向即语义，无
// 文字文案；悬停提示只保可发现性，不占版面。初始态由打开入口给定
// （openDiffAs），reloadDiff 按此态恢复展开。
func (ws *workspace) expandToggle(c *ui.Context) {
	t := c.Theme()
	tip := "展开未修改"
	if ws.diffv.expand {
		tip = "收起未修改"
	}
	b := ui.ButtonBase(c).Key("expand-unchanged").Size(24, 24).Radius(4).Label(tip).Tooltip(tip).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(t.Text.Alpha(0.08))
	}
	// 尖角画法沿用官方分隔条的 ExpandAll 图标（跨度 d=3.5、描边 1.5）：
	// 收起态尖点朝内停在与展开态开口相同的位置（cy∓2），开口端外移到
	// cy∓(d+2)，整体高度与展开态一致且不交叉
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy, d := r.X+r.W/2, r.Y+r.H/2, float32(3.5)
		var path ui.Path
		if ws.diffv.expand {
			path.MoveTo(cx-d, cy-d-2).LineTo(cx, cy-2).LineTo(cx+d, cy-d-2)
			path.MoveTo(cx-d, cy+d+2).LineTo(cx, cy+2).LineTo(cx+d, cy+d+2)
		} else {
			path.MoveTo(cx-d, cy-2).LineTo(cx, cy-2-d).LineTo(cx+d, cy-2)
			path.MoveTo(cx-d, cy+2).LineTo(cx, cy+2+d).LineTo(cx+d, cy+2)
		}
		p.StrokePath(&path, 1.5, t.TextMuted)
	})
	if b.Clicked() {
		ws.diffv.expand = !ws.diffv.expand
		if ws.diffv.expand {
			ws.diffv.ed.ExpandAll()
		} else {
			ws.diffv.ed.CollapseAll()
		}
	}
}

// closeDiff 返回主区。
func (ws *workspace) closeDiff() {
	ws.diffv.open = nil
	ws.diffv.sides = nil
	ws.diffv.jumpLine = 0
}

// reloadDiff 重新加载当前打开的 diff（写盘或暂存操作后）；保留当前展开态，
// 不重复定位（定位只属于打开请求）。
func (ws *workspace) reloadDiff() {
	if ws.diffv.open != nil {
		ws.openDiffAs(*ws.diffv.open, ws.diffv.expand, 0)
	}
}

// runWrite 统一写操作的执行与收尾：经管线后台执行、主线程收尾；失败写
// setErr，成功清空并跑 after（触发方各自刷新关联视图）。
func (ws *workspace) runWrite(op func() error, label string, after func()) {
	loadAsync(ws, op, func(err error) {
		if err != nil {
			ws.setErr(label, err.Error())
			return
		}
		ws.clearErr()
		if after != nil {
			after()
		}
	})
}

// revertHunkText 把 hunk 的旧侧行放回新文本（其余行保持）。
// Split 结果的末尾空串代表原文以换行结尾，Join 后自动保持两侧的末行换行状态。
func revertHunkText(oldText, newText string, h editor.DiffHunk) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	out := make([]string, 0, len(newLines)-(h.NewEnd-h.NewStart)+(h.OldEnd-h.OldStart))
	out = append(out, newLines[:h.NewStart]...)
	out = append(out, oldLines[h.OldStart:h.OldEnd]...)
	out = append(out, newLines[h.NewEnd:]...)
	return strings.Join(out, "\n")
}

// revertDiffHunk 把一个改动块还原到磁盘（staged 只读不可还原）。
// untracked 还原为空内容即删除文件。
func (ws *workspace) revertDiffHunk(h editor.DiffHunk) {
	v := ws.diffv.open
	if v == nil || v.Section == "staged" || ws.diffv.sides == nil {
		return
	}
	content := revertHunkText(ws.diffv.sides.oldValue, ws.diffv.sides.newValue, h)
	ws.runWrite(func() error {
		if v.Section == "untracked" && content == "" {
			return git.RemoveWorktreeFile(v.RepoPath, v.Path)
		}
		return git.WriteWorktreeFile(v.RepoPath, v.Path, content)
	}, "还原失败", nil)
}

// renderHunkActions 是官方 diff 视图的改动块动作条：只提供落盘还原。
// 组件自带的 Keep 只改内存缓冲，会让视图与磁盘脱节，不提供。
// 样式对齐官方 DefaultHunkActions：动作条是绝对定位的 Row，没有高度上限，
// 主题 Button 的默认内边距会把它撑高盖住代码行——ButtonBase 紧凑条才贴得进
// 代码右侧的悬浮条；字号跟编辑器字体（max(11, FontSize-2)），不取应用令牌。
func (ws *workspace) renderHunkActions(c *ui.Context, bar ui.Element, a *editor.DiffHunkActions) {
	t := c.Theme()
	bar.Row().Gap(t.Space(1))
	bar.Children(func() {
		btn := ui.ButtonBase(c).Padding(0, t.Space(2)).Radius(t.Radius-2).
			Background(t.Surface).Border(1, t.Border).Label("还原此块")
		btn.Children(func() {
			ui.Text(c, "还原此块").FontSize(max(11, t.FontSize-2))
		})
		if btn.Clicked() {
			ws.revertDiffHunk(a.Hunk)
		}
	})
}

// toggleStageFile 暂存/取消暂存一个文件（diff 头部与列表行共用）。
// 不要求存在打开会话：完成后仅当仍是发起时的会话（世代未变）才翻转
// 打开中的 diff。
func (ws *workspace) toggleStageFile(v git.Change) {
	seq := ws.diffv.seq
	ws.runWrite(func() error {
		if v.Section == "staged" {
			return git.UnstageFiles(v.RepoPath, []string{v.Path})
		}
		return git.StageFiles(v.RepoPath, []string{v.Path})
	}, "暂存操作失败", func() {
		// 打开中的 diff 跟着切到新分区（同一会话内），并刷新列表；
		// 期间换开别的 diff（世代已变）则不再动新会话
		if ws.diffv.open != nil && ws.diffv.seq == seq {
			ws.diffv.flip()
			ws.reloadDiff()
		}
		ws.rescan(ws.root)
	})
}

// diffView 是打开中的 diff：头部（左侧路径+状态两行，按钮组靠右）与内容。
func (ws *workspace) diffView(c *ui.Context) {
	tk := appTokens(c)
	v := ws.diffv.open
	ui.Column(c).Fill().Children(func() {
		// 头部左两行右按钮：第一行路径、第二行状态（纯文本小字，元信息不与
		// 按钮抢视觉）；按钮组占满行高、垂直居中。路径 Grow 占剩余宽度且
		// ClipX 放弃 min-content 地板：宽度不够时路径中间截断让位，按钮组
		// 不收缩
		ui.Row(c).FillWidth().Height(48).PaddingX(8).Gap(8).AlignItems(ui.Center).Children(func() {
			isMD := isMarkdownPath(v.Path)
			ui.Column(c).Grow(1).FillWidth().Gap(2).Children(func() {
				ui.Text(c, v.Path).Font(monoFont()).FontSize(tk.FontMD).SingleLine().TruncateMiddle().ClipX()
				ui.Text(c, sectionLabel(v.Section)).FontSize(tk.FontSM).TextColor(tk.Muted)
			})
			// 内容模式组：「查看 diff」开关只在目录入口的 diff 头部显示
			// （diff 列表打开的 diff 与该开关无关）；预览是 md 的渲染模式切换
			if ws.diffv.fromDir {
				ws.dirShowDiffCheckbox(c)
			}
			if isMD && ws.diffv.sides != nil && !ws.diffv.sides.binary {
				// 渲染模式切换用开关（Segmented），不用会改文案的按钮
				ws.mdPreviewSwitch(c, "预览", "差异")
			}
			if ws.diffv.fromDir || isMD {
				ui.Divider(c)
			}
			// 浏览工具组：布局形态（统一/分栏分段切换，与历史详情共用
			// ws.diffSeg）→ 内容范围（展开未修改）。查找没有按钮：编辑器
			// keymap 自带 Cmd/Ctrl+F（keymap.go），焦点在 diff 内容区即生效
			layoutSwitch(c, ws)
			// 展开未修改的图标开关（仅文本 diff 有意义）：收起态上下尖角
			// 外扩=点击全展开，展开态尖角相向=点击收起；打开时的初始态
			// 由入口决定（openDiffAs）
			if ws.diffv.sides != nil && !ws.diffv.sides.binary {
				ws.expandToggle(c)
			}
			// git 操作组：暂存是写操作，与只读的浏览工具分开
			ui.Divider(c)
			label := "暂存"
			if v.Section == "staged" {
				label = "取消暂存"
			}
			if ui.Button(c, label).Clicked() {
				ws.toggleStageFile(*v)
			}
			if ui.Button(c, "复制路径").Clicked() {
				c.WriteClipboard(changeAbs(*v))
			}
		})

		ui.Divider(c)

		switch {
		case ws.diffv.loading:
			emptyState(c, "加载中…", "")
		case ws.diffv.sides != nil && ws.diffv.sides.binary:
			if ws.diffv.bitmap != nil {
				// 二进制图片：展示当前版本（列占头部之下剩余高度用 Grow，坑 14）
				ui.Column(c).FillWidth().Grow(1).Background(tk.Surface).Children(func() {
					ui.Image(c, ws.diffv.bitmap).Fit(ui.Contain).Fill()
				})
			} else {
				ui.Column(c).Fill().Center().Gap(4).Children(func() {
					ui.Text(c, "二进制文件").FontSize(tk.FontLG).FontWeight(600)
					ui.Text(c, "无法以文本方式比较，可用头部按钮暂存或取消暂存").TextColor(tk.Muted).FontSize(tk.FontSM)
				})
			}
		case ws.mdPreview && ws.diffv.sides != nil:
			// Markdown 预览：渲染工作区（新侧）内容
			markdownPreview(c, ws.diffv.sides.newValue, &ws.mdOpts)
		case ws.diffv.sides != nil && ws.diffv.sides.oldValue == ws.diffv.sides.newValue:
			emptyState(c, "两侧内容一致", "")
		case ws.diffv.sides != nil:
			// 布局在渲染帧统一落值：切换偏好即帧内生效，也免去在切换
			// 控件处逐视图回写
			ws.diffv.ed.Layout = diffLayout(ws.diffSeg)
			editor.DiffView(c, &ws.diffv.ed).Fill()
			// 搜索结果定位排在 diff 构建之后：分栏的共享滚动由构建过程
			// 装配，先滚会落在空的共享滚动上被钳回 0。等布局量过一帧
			// （视口高度）再滚，首帧的行高还没量好
			if line := ws.diffv.jumpLine; line > 0 && ws.diffv.ed.ViewportHeight() > 0 {
				ws.jumpDiffLine(line)
			}
		}
	})
}

// jumpDiffLine 把 diff 滚到新侧第 line 行（1 起），让它在视口中间：内容
// 命中的行号是工作区文件的（新侧），统一/分栏都由 LineTop 换算到行顶。
func (ws *workspace) jumpDiffLine(line int) {
	d := &ws.diffv.ed
	if n := d.New.LineCount(); n > 0 {
		line = min(max(line, 1), n)
	}
	d.SetScrollY(d.LineTop(editor.DiffNew, line-1) - d.ViewportHeight()/2)
	ws.diffv.jumpLine = 0
}
