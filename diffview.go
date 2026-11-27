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

// diffSides 是一个改动文件的两侧文本；binary 时文本为空（binOld/binNew
// 带两侧字节供图片对比，非图片二进制两侧为空）。
type diffSides struct {
	oldValue string
	newValue string
	binary   bool
	binOld   []byte
	binNew   []byte
}

// sidesContentEqual 判断两侧内容是否一致（含二进制的两侧字节）；任一
// 侧未加载视为不等——equality 只对「已应用 vs 重读」这一组有意义。
func sidesContentEqual(a, b *diffSides) bool {
	if a == nil || b == nil {
		return false
	}
	return a.binary == b.binary && a.oldValue == b.oldValue &&
		a.newValue == b.newValue && bytes.Equal(a.binOld, b.binOld) &&
		bytes.Equal(a.binNew, b.binNew)
}

// loadSides 读取一个改动的两侧文本：
// unstaged 用 index 对工作区，staged 用 HEAD 对 index，untracked 直接看工作区。
// 二进制图片时读取两侧字节（旧侧从 git 对象取，新侧取工作区），供并排对比。
func loadSides(v git.Change) *diffSides {
	s := &diffSides{}
	// oldPath 是旧侧在 git 对象里的路径：改名/复制的旧内容在 OrigPath。
	// porcelain v1 目前不在工作区侧报 R（拆成 D + untracked），unstaged
	// 分支通常等于 Path；统一口径，口径变化时旧侧仍正确
	oldPath := v.Path
	if v.OrigPath != "" {
		oldPath = v.OrigPath
	}
	// showObjectBytes 读对象字节：ShowObject 的 string 经 Run 的 bytes.Buffer
	// 原样保留二进制内容，转换无损，不为此加 git 包的 []byte 变体
	showObjectBytes := func(rev, path string) []byte {
		out, found, err := git.ShowObject(v.RepoPath, rev, path)
		if err != nil || !found {
			return nil
		}
		return []byte(out)
	}
	// fillBinary 只给图片扩展名带字节：非图片二进制维持「二进制文件」空态。
	// 两侧都按分区语义取——staged 是 HEAD 对 index（旧侧在 HEAD，新侧在
	// index 而非工作区），unstaged 是 index 对工作区（旧侧在 index，删除时
	// 工作区无文件新侧为空）。worktree 是工作区当前内容（nil=不可读或不适用）
	fillBinary := func(worktree []byte) {
		s.binary = true
		if !isImagePath(v.Path) {
			return
		}
		if v.Section == "staged" {
			s.binOld = showObjectBytes("HEAD", oldPath)
			s.binNew = showObjectBytes(":0", v.Path)
			return
		}
		s.binOld = showObjectBytes(":0", oldPath)
		s.binNew = worktree
	}
	switch v.Section {
	case "untracked":
		content, found, err := git.ReadWorktreeFile(v.RepoPath, v.Path)
		if err != nil || !found {
			return s
		}
		// 未跟踪文件没有 diff 可问，直接看内容里有没有 NUL；新侧即工作区内容
		if isBinary(content) {
			fillBinary([]byte(content))
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
			fillBinary(nil)
			return s
		}
		// 新文件暂存后 HEAD 没有该对象，旧侧为空；改名/复制的旧内容在
		// 旧路径（HEAD:OrigPath），新侧按现路径取
		s.oldValue, _, _ = git.ShowObject(v.RepoPath, "HEAD", oldPath)
		s.newValue, _, _ = git.ShowObject(v.RepoPath, ":0", v.Path)
		return s
	default:
		var err error
		if s.binary, err = git.IsBinaryDiff(v.RepoPath, v.Path, false); err != nil {
			s.binary = false
		}
		if s.binary {
			data, _ := os.ReadFile(changeAbs(v)) // 删除时不存在，新侧留空
			fillBinary(data)
			return s
		}
		s.oldValue, _, _ = git.ShowObject(v.RepoPath, ":0", oldPath)
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
// 递增 seq（世代），在途加载凭世代落地（最新打开者胜），flip/adopt 是
// 同一会话内的原地改写、世代不变。唯一写点：openDiffAs/applyDiffState
// 的打开与加载、toggleStageFile 的分区翻转、flip/adopt 的会话对齐、
// closeDiff/reloadDiff、渲染帧里的布局与展开开关。
type diffPane struct {
	open *git.Change // 当前打开的 diff（nil=未打开）
	// seq 是打开会话世代：openDiffAs 递增并捕获，applyDiffState 校验
	// 相等才落地。替代旧的结构体值相等判定——不依赖 git.Change 可比较
	//（加非比较字段即编译炸），且 Code/RepoRel 等展示字段不参与同一性
	seq     int
	loading bool
	sides   *diffSides
	// bitmapOld/bitmapNew 是二进制图片两侧的解码结果（nil=该侧无字节或
	// 解码失败，渲染时按占位提示处理）
	bitmapOld *ui.Bitmap
	bitmapNew *ui.Bitmap
	// imgZoom 是图片对比两列共享的缩放/平移状态（两列传同一指针即联动）；
	// 内容变化时在 applyDiffState 归位
	imgZoom imageZoom
	// zoomHandle 绑定新版图片列（一个 Handle 只能绑一个控件，旧版列靠
	// 点击聚焦），打开请求借此把键盘焦点送进内容区；focusPending 标记
	// 这次打开还没落过焦点（watch 同内容重读不重复抢）
	zoomHandle   ui.Handle
	focusPending bool
	// expand 是当前打开 diff 的展开态：打开时取全局偏好（diffPrefs().Expand），
	// 头部「展开未修改」开关翻转后写回全局偏好并落盘
	expand bool
	// fromDir 标记是否目录列表入口打开：「查看 diff」开关翻转时只切换
	// 目录入口的内容，diff 列表打开的 diff 不受该开关管辖
	fromDir bool
	// jumpLine 是新侧要定位到的行（1 起，0 = 无）：搜索结果的内容命中
	// 打开 diff 时带上，布局量好后（下一帧）滚动一次并清零
	jumpLine int
	// ed 是官方 diff 视图的跨帧状态（缓冲、布局、展开的上下文与滚动位置），
	// 查找条与还原按钮也由它承载；布局偏好是全局配置（diffPrefs().Layout），
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

// adopt 把会话对齐到模型里的当前条目（同一文件的分区被外部改变）：
// 原地改写、世代不变，重读由 reloadDiff 走加载管线判定内容。
func (d *diffPane) adopt(v git.Change) {
	if d.open == nil {
		return
	}
	*d.open = v
}

// openDiff 打开一个文件的 diff（diff 列表入口）：先置占位再后台加载两侧。
// 展开态取全局偏好（diffPrefs().Expand）。帧内调用时改状态即触发重建，
// 无需 Invalidate。
func (ws *workspace) openDiff(v git.Change) {
	ws.diffv.fromDir = false
	ws.openDiffAs(v, 0)
}

// openDiffFromDir 是目录列表入口：文件带改动时直接展示 diff；「查看 diff」
// 开关翻转时只切换这类内容（fromDir）。展开态与布局同样取全局偏好。
func (ws *workspace) openDiffFromDir(v git.Change) {
	ws.openDiffFromDirAt(v, 0)
}

// openDiffFromDirAt 是 openDiffFromDir 的带定位版本：打开后滚到新侧第
// line 行（1 起，0 = 从头），搜索结果的内容命中靠它定位。
func (ws *workspace) openDiffFromDirAt(v git.Change, line int) {
	ws.diffv.fromDir = true
	ws.openDiffAs(v, line)
}

func (ws *workspace) openDiffAs(v git.Change, line int) {
	ws.dirs.file = nil // 主区以最后点击的为准：打开 diff 即让位出文件查看
	// 预览模式不记忆：每次打开都是差异视图，用户手动切预览
	ws.mdPreview = false
	ws.diffv.open = &v
	ws.diffv.seq++
	seq := ws.diffv.seq
	// 展开未修改是全局偏好：打开只读配置，不写盘
	ws.diffv.expand = ws.diffPrefs().Expand
	ws.diffv.loading = true
	ws.diffv.sides = nil
	ws.diffv.jumpLine = line
	// 焦点随打开请求进内容区：落地后由 applyDiffState 消费（图片给图片
	// 列，文本给官方 diff 编辑器）
	ws.diffv.focusPending = true

	loadAsync(ws, func() *diffSides { return loadSides(v) },
		func(sides *diffSides) { ws.applyDiffState(seq, v, sides) })
}

func (ws *workspace) applyDiffState(seq int, v git.Change, sides *diffSides) {
	// 只接受仍是当前打开会话的结果（最新打开者胜；翻转不改世代）
	if ws.diffv.open == nil || ws.diffv.seq != seq {
		return
	}
	// 分区决定写能力（staged 只读无还原），不能随内容比对跳过：暂存/取消
	// 暂存或外部改变分区后两侧文本可能碰巧相同（HEAD→工作区 与 HEAD→
	// index 同为一份内容），能力仍须跟着新分区走
	ws.diffv.ed.HunkActions = v.Section != "staged"
	// 两侧内容没变（watch 重读）：不动已建缓冲与解码结果——滚动、展开态
	// 与图片都属于已打开的会话，重复重建会让界面持续闪烁
	if sidesContentEqual(ws.diffv.sides, sides) {
		return
	}
	ws.diffv.sides = sides
	ws.diffv.loading = false
	ws.diffv.bitmapOld = nil
	ws.diffv.bitmapNew = nil
	// 内容变化即归位图片缩放：缩放属于旧内容，新内容从适配态重新看
	ws.diffv.imgZoom.reset()
	if ws.diffv.jumpLine > 0 {
		// 定位等布局量好行高后的下一帧：请求重绘，别让跳转停在悬空状态
		ws.app.invalidate()
	}
	// Markdown 预览的本地图片按文件所在目录解析
	ws.mdOpts.LoadImage = markdown.FileImages(dirOf(changeAbs(v)))
	// 焦点随打开请求进内容区，落地时一次性消费（Focus 的 pending 语义
	// 覆盖控件还没 build 的时序）；watch 重读早退时不动它
	takeFocus := ws.diffv.focusPending
	ws.diffv.focusPending = false
	if sides.binary {
		// 图片在状态应用时解码一次（帧内不做 IO/解码）；单侧解码失败不
		// 影响另一侧展示
		if len(sides.binOld) > 0 {
			if bmp, err := ui.DecodeBitmap(sides.binOld); err == nil {
				ws.diffv.bitmapOld = bmp
			}
		}
		if len(sides.binNew) > 0 {
			if bmp, err := ui.DecodeBitmap(sides.binNew); err == nil {
				ws.diffv.bitmapNew = bmp
			}
		}
		// 只有真的渲染出图片才有可聚焦的内容：两侧都解码失败时请求作废，
		// 免得遗留的 pending 在之后别的会话上突然生效
		if takeFocus && (ws.diffv.bitmapOld != nil || ws.diffv.bitmapNew != nil) {
			ws.diffv.zoomHandle.Focus()
		}
		return
	}
	// 文本 diff：官方 API 把键盘给活动侧编辑器（下次构建生效），方向键
	// 滚动、Cmd+F 查找打开即用
	if takeFocus {
		ws.diffv.ed.Focus()
	}
	// 官方 diff 视图：统一/分栏、折叠上下文、内置查找；staged 只读无还原。
	// ExpandUnchanged 取打开时的全局偏好（diffPane.expand）
	// （布局在渲染帧按全局偏好 diffPrefs().Layout 写入，打开时不定）
	ws.diffv.ed.Old = editor.NewBuffer(sides.oldValue)
	ws.diffv.ed.New = editor.NewBuffer(sides.newValue)
	lang := editor.LanguageFor(v.Path) // 未知扩展名返回 nil，按纯文本
	ws.diffv.ed.Old.SetLanguage(lang)
	ws.diffv.ed.New.SetLanguage(lang)
	ws.diffv.ed.ExpandUnchanged = ws.diffv.expand
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
		// 展开未修改是全局偏好：显式切换即写内存并落盘
		ws.diffPrefs().Expand = ws.diffv.expand
		ws.app.saveConfig()
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

// reconcileOpenDiff 让打开中的 diff 跟随改动模型，写点在模型变更后的
// 统一收尾（applyChangedModel）：
//   - 文件仍在同一分区：不动会话——滚动、展开与缓冲属于已打开的内容；
//   - 分区被外部改变（暂存/取消暂存/提交）：对齐到模型里的当前条目并
//     重读，头部徽标、可写性与两侧内容都以该条目为准；
//   - 文件已不在改动列表（外部还原/提交/删除）：目录入口且文件还在
//     磁盘上时回落只读查看，否则关闭会话回到未选择文件的空态。
func (ws *workspace) reconcileOpenDiff() {
	open := ws.diffv.open
	if open == nil {
		return
	}
	for i := range ws.changes {
		if fileID(ws.changes[i]) == fileID(*open) {
			return
		}
	}
	for i := range ws.changes {
		if sameChange(ws.changes[i], *open) {
			ws.diffv.adopt(ws.changes[i])
			ws.reloadDiff()
			return
		}
	}
	// 目录入口的内容：文件仍在磁盘上就回落只读查看（用户从目录树来）；
	// 已被删除则没有可看的内容，回到未选择空态
	if abs := ws.diffv.target(); ws.diffv.fromDir && fileExists(abs) {
		ws.openFile(abs)
		return
	}
	ws.closeDiff()
}

// reloadDiff 重新加载当前打开的 diff（watch 命中或暂存切换后）；不走
// openDiffAs 整体重开——重开会清空 sides 走 loading（闪断）并重建缓冲
// （滚动、展开态丢失），而这些属于已打开的会话；两侧内容是否真的变化由
// applyDiffState 判定，相同直接丢弃。定位只属于打开请求，重载不重复定位。
func (ws *workspace) reloadDiff() {
	if ws.diffv.open == nil {
		return
	}
	v := *ws.diffv.open
	ws.diffv.seq++
	seq := ws.diffv.seq
	loadAsync(ws, func() *diffSides { return loadSides(v) },
		func(sides *diffSides) { ws.applyDiffState(seq, v, sides) })
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

// sameChange 判断两个改动是否指向同一文件（分区不参与：翻转前后分区会变）。
func sameChange(a, b git.Change) bool {
	return a.RepoPath == b.RepoPath && a.Path == b.Path
}

// toggleStageFile 暂存/取消暂存一个文件（列表行右键）。不要求存在打开
// 会话：完成后仅当打开中的正是该文件（世代未变）才翻转它。
func (ws *workspace) toggleStageFile(v git.Change) {
	seq := ws.diffv.seq
	ws.runWrite(func() error {
		if v.Section == "staged" {
			return git.UnstageFiles(v.RepoPath, []string{v.Path})
		}
		return git.StageFiles(v.RepoPath, []string{v.Path})
	}, "暂存操作失败", func() {
		// 打开中的 diff 跟着切到新分区（同一会话、同一文件），并刷新列表；
		// 期间换开别的 diff（世代已变）或打开的是别的文件时不动它
		if ws.diffv.open != nil && ws.diffv.seq == seq && sameChange(*ws.diffv.open, v) {
			ws.diffv.flip()
			ws.reloadDiff()
		}
		ws.rescan(ws.root)
	})
}

// stageDir 暂存一个目录下的全部改动。目录必须归属单一仓库（含仓库根）：
// 跨仓库的目录（仓库 rel 的虚拟父层、包含多个仓库的文件系统目录）没有
// 可表达的 git 路径，菜单不提供该动作。收集按 ws.changes 的仓库内前缀，
// 不用 git add <dir>——项目允许根仓库内嵌子仓库，目录 add 会把未登记的子
// 仓库当 gitlink 加进来。
func (ws *workspace) stageDir(abs string) {
	repo := ws.repoForAbs(abs)
	if repo == "" {
		return
	}
	prefix := abs + "/"
	seen := map[string]bool{}
	var paths []string
	for i := range ws.changes {
		c := &ws.changes[i]
		if c.RepoPath != repo {
			continue
		}
		ca := changeAbs(*c)
		if ca != abs && !strings.HasPrefix(ca, prefix) {
			continue
		}
		if seen[ca] {
			continue
		}
		seen[ca] = true
		paths = append(paths, c.Path)
	}
	if len(paths) == 0 {
		return
	}
	// 打开中的 diff 在本次范围内且原本不在 staged 区：暂存后它只留在
	// staged 区，会话内翻转并重读；不在范围内或已 staged 都不动它
	seq := ws.diffv.seq
	var open git.Change
	flipOpen := false
	if o := ws.diffv.open; o != nil && o.Section != "staged" {
		open = *o
		flipOpen = seen[changeAbs(open)]
	}
	ws.runWrite(func() error {
		return git.StageFiles(repo, paths)
	}, "暂存操作失败", func() {
		if flipOpen && ws.diffv.seq == seq && ws.diffv.open != nil && sameChange(*ws.diffv.open, open) {
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
			// 浏览工具组：布局形态（统一/分栏，全局偏好）→ 内容范围
			//（展开未修改，全局偏好）。图片 diff 不参与布局与展开，两侧
			// 控件都不渲染；查找没有按钮：编辑器 keymap 自带 Cmd/Ctrl+F
			//（keymap.go），焦点在 diff 内容区即生效
			if ws.diffv.sides == nil || !ws.diffv.sides.binary {
				ws.diffLayoutSwitch(c)
			}
			// 展开未修改的图标开关（仅文本 diff 有意义）：收起态上下尖角
			// 外扩=点击全展开，展开态尖角相向=点击收起；打开时的初始态
			// 取全局偏好，切换即写回并落盘
			if ws.diffv.sides != nil && !ws.diffv.sides.binary {
				ws.expandToggle(c)
			}
		})

		ui.Divider(c)

		switch {
		case ws.diffv.loading:
			emptyState(c, "加载中…", "")
		case ws.diffv.sides != nil && ws.diffv.sides.binary:
			// 二进制：图片旧/新两侧并排对比（新增/删除单侧占满），非图片维持空态
			ws.binaryDiffView(c, tk)
		case ws.mdPreview && ws.diffv.sides != nil:
			// Markdown 预览：渲染工作区（新侧）内容
			markdownPreview(c, ws.diffv.sides.newValue, &ws.mdOpts)
		case ws.diffv.sides != nil && ws.diffv.sides.oldValue == ws.diffv.sides.newValue:
			emptyState(c, "两侧内容一致", "")
		case ws.diffv.sides != nil:
			// 布局在渲染帧统一落值：切换偏好即帧内生效，也免去在切换
			// 控件处逐视图回写
			ws.diffv.ed.Layout = diffLayout(ws.diffPrefs().Layout)
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

// binaryDiffView 是二进制 diff 的内容区：图片扩展名时旧/新两侧并排对比
// （新增/删除单侧时占满全宽），非图片二进制或两侧字节都读不到时维持文字
// 空态。每列是标签行（版本 + 尺寸·大小）+ Surface 图片区；字节在但解码
// 失败的一侧渲染占位文案，不挡住另一侧。
func (ws *workspace) binaryDiffView(c *ui.Context, tk tokens) {
	sides := ws.diffv.sides
	oldOk := ws.diffv.bitmapOld != nil || len(sides.binOld) > 0
	newOk := ws.diffv.bitmapNew != nil || len(sides.binNew) > 0
	if !oldOk && !newOk {
		ui.Column(c).Fill().Center().Gap(4).Children(func() {
			ui.Text(c, "二进制文件").FontSize(tk.FontLG).FontWeight(600)
			ui.Text(c, "无法以文本方式比较，可在侧栏列表右键暂存或取消暂存").TextColor(tk.Muted).FontSize(tk.FontSM)
		})
		return
	}
	// 列占头部之下剩余高度用 Grow 不用 Fill（坑 14）；Row 交叉轴显式
	// Stretch：默认对齐会把 pane 列垂直居中，图片区高度塌成标签行高
	//（headless 探针实抓）
	ui.Row(c).FillWidth().Grow(1).AlignItems(ui.Stretch).Children(func() {
		side := func(label string, bmp *ui.Bitmap, data []byte, handle *ui.Handle) {
			ui.Column(c).FillWidth().Grow(1).Children(func() {
				ui.Row(c).FillWidth().Height(tk.Space(5)).PaddingX(tk.Space(2)).Gap(tk.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).FontSize(tk.FontXS).TextColor(tk.Muted)
					if bmp != nil {
						w, h := bmp.Size()
						ui.Text(c, fmt.Sprintf("%d×%d · %s", w, h, formatBytes(len(data)))).FontSize(tk.FontXS).TextColor(tk.Muted)
					}
				})
				ui.Column(c).FillWidth().Grow(1).Background(tk.Surface).Children(func() {
					if bmp != nil {
						// 两列传同一状态指针：滚轮/拖拽作用任一列都联动。
						// handle 只绑实际渲染出图片的一列（一个 Handle 绑
						// 两个控件会 panic），由调用方择一传入
						zoomImageView(c, bmp, &ws.diffv.imgZoom, handle, label+"图片")
						return
					}
					ui.Row(c).Fill().Center().Children(func() {
						ui.Text(c, "无法解码该图片").TextColor(tk.Muted).FontSize(tk.FontSM)
					})
				})
			})
		}
		// 焦点 Handle 绑到真正渲染出图片的列：优先新版；删除图片只剩旧版
		// 时绑旧版，否则打开请求的聚焦永远悬空
		var oldHandle, newHandle *ui.Handle
		switch {
		case ws.diffv.bitmapNew != nil:
			newHandle = &ws.diffv.zoomHandle
		case ws.diffv.bitmapOld != nil:
			oldHandle = &ws.diffv.zoomHandle
		}
		if oldOk {
			side("旧版", ws.diffv.bitmapOld, sides.binOld, oldHandle)
		}
		if oldOk && newOk {
			ui.Divider(c)
		}
		if newOk {
			side("新版", ws.diffv.bitmapNew, sides.binNew, newHandle)
		}
	})
}

// formatBytes 字节数的人类可读格式（B/KB/MB），供图片对比的标签行。
func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
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
