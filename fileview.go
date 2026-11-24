package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

// fileView 是主区打开的工作区文件（只读查看）。
type fileView struct {
	abs     string
	loading bool
	kind    string // text / image / unsupported
	// content 是最近一次应用的原始字节：watch 重读据此比对，相同则不动
	// 已建状态（文本的编辑器/预览、图片的解码结果）
	content string
	// line 是打开请求要跳到的行（1 起，0 = 不跳）：搜索内容命中的点击带
	// 行号；跳到即清零，之后的 watch 重载不再重复跳
	line   int
	bitmap *ui.Bitmap
	// ed 是文本文件的官方编辑器状态（只读、按扩展名高亮），跨帧保持
	ed *editor.State
}

// fileMainView 是打开中的工作区文件：头部（路径居左，按钮全部靠右）+ 内容。
func (ws *workspace) fileMainView(c *ui.Context) {
	tk := appTokens(c)
	v := ws.dirs.file
	isMd := isMarkdownPath(v.abs)
	ui.Column(c).Fill().Children(func() {
		// 行高与 diff 头一致（48，diffview.go）：两个视图切换时头部不跳变；
		// 路径 Grow 占剩余宽度且 ClipX 放弃 min-content 地板，宽度不够时
		// 路径中间截断让位，按钮组不收缩
		ui.Row(c).FillWidth().Height(48).PaddingX(8).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, relPath(ws.root, v.abs)).Font(monoFont()).FontSize(tk.FontMD).Grow(1).SingleLine().TruncateMiddle().ClipX()
			if isMd && v.kind == "text" {
				// 渲染模式切换用开关（Segmented），不用会改文案的按钮
				ws.mdPreviewSwitch(c, "预览", "源码")
			}
			// 「查看 diff」只对带改动的文件有意义：无改动没有 diff 可看
			if ws.changeForAbs(v.abs) != nil {
				ws.dirShowDiffCheckbox(c)
			}
		})
		ui.Divider(c)

		switch {
		case v.loading:
			emptyState(c, "加载中…", "")
		case v.kind == "image" && v.bitmap != nil:
			// 非 Clip/滚动的列不许收缩（min-content=图片自然高），高图会把
			// 列底撑出 pane；Grow 才是「头部之下的剩余高度」契约（坑 14）
			ui.Column(c).FillWidth().Grow(1).Center().Background(tk.Surface).Children(func() {
				ui.Image(c, v.bitmap).Fit(ui.Contain).Fill()
			})
		case v.kind == "unsupported":
			ui.Column(c).Fill().Center().Gap(4).Children(func() {
				ui.Text(c, "不支持打开该文件").FontSize(tk.FontLG).FontWeight(600)
				ui.Text(c, "二进制内容，无法作为文本或图片预览").TextColor(tk.Muted).FontSize(tk.FontSM)
			})
		case isMd && ws.mdPreview:
			markdownPreview(c, v.content, &ws.mdOpts)
		default:
			editor.View(c, v.ed).Fill()
		}
	})
}

func isMarkdownPath(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".md") || strings.HasSuffix(strings.ToLower(path), ".markdown")
}

func dirOf(abs string) string {
	idx := strings.LastIndex(abs, "/")
	if idx <= 0 {
		return abs
	}
	return abs[:idx]
}

// openFile 打开一个工作区文件：「查看 diff」勾选且带改动时直接展示 diff
// （默认全展开），否则只读查看原内容；文件属于某仓库时同步当前仓库。
func (ws *workspace) openFile(abs string) {
	ws.openFileAt(abs, 0)
}

// reloadOpenFile 是 watch 命中后的当前文件重读：不走 openFileAt 重新打开
// ——重新打开会换全新的 fileView（loading 闪断、滚回顶部、Markdown 预览
// 被踢回源码），而这些状态属于已打开的会话；内容是否真的变化由 applyFile
// 判定，相同直接丢弃。路由（「查看 diff」）只属于打开请求，重读不重路由。
func (ws *workspace) reloadOpenFile() {
	if ws.dirs.file == nil {
		return
	}
	abs := ws.dirs.file.abs
	loadAsync(ws, func() fileResult {
		content, err := os.ReadFile(abs)
		return fileResult{content: content, err: err}
	}, func(r fileResult) { ws.applyFile(abs, r.content, r.err) })
}

// openFileAt 是 openFile 的带跳转版本：打开后跳到 line 行（1 起，0 = 从头
// 显示），搜索结果的内容命中靠它定位。
func (ws *workspace) openFileAt(abs string, line int) {
	if repo := ws.repoForAbs(abs); repo != "" {
		ws.switchActiveRepo(repo)
	}
	if ws.dirs.showDiff {
		if ch := ws.changeForAbs(abs); ch != nil {
			ws.openDiffExpandedAt(*ch, line)
			return
		}
	}
	ws.closeDiff() // 主区以最后点击的为准：打开文件即让位出 diff（否则会一直显示旧 diff）
	// 预览模式不记忆：每次打开文件都是源码，用户手动切预览（否则上一个
	// 文件的预览态会带到下一个文件）
	ws.mdPreview = false
	ws.dirs.file = &fileView{abs: abs, loading: true, line: line}
	loadAsync(ws, func() fileResult {
		content, err := os.ReadFile(abs)
		return fileResult{content: content, err: err}
	}, func(r fileResult) { ws.applyFile(abs, r.content, r.err) })
}

// fileResult 是文件读取管线的加载结果。
type fileResult struct {
	content []byte
	err     error
}

// retargetOpened 把主区已打开的内容按「查看 diff」新开关切换：勾选时目录
// 列表打开的只读文件有改动就切到 diff；取消时目录入口打开的 diff 切回只读
// 查看。不属于开关管辖的内容（diff 列表入口的 diff、无改动文件）不动。
func (ws *workspace) retargetOpened() {
	switch {
	case ws.dirs.showDiff:
		if ws.dirs.file != nil && ws.changeForAbs(ws.dirs.file.abs) != nil {
			ws.openFile(ws.dirs.file.abs)
		}
	case ws.diffv.open != nil && ws.diffv.fromDir:
		ws.openFile(ws.diffv.target())
	}
}

// applyFile 应用文件读取结果；期间已关闭/换文件时作废。
func (ws *workspace) applyFile(abs string, content []byte, err error) {
	if ws.dirs.file == nil || ws.dirs.file.abs != abs {
		return // 期间已关闭/换文件
	}
	// watch 重载时内容没变就不动状态：队列投递每次都重建帧，
	// 重复应用相同内容会让界面持续闪烁
	head := content
	if len(head) > 8192 {
		head = head[:8192]
	}
	binary := isBinary(string(head))
	same := err == nil && !ws.dirs.file.loading && ws.dirs.file.kind != "" && ws.dirs.file.content == string(content)
	if same {
		// 内容没变（watch 重载）：仍按新请求跳到目标行
		ws.jumpToFileLine()
		return
	}
	ws.dirs.file.loading = false
	if err != nil {
		ws.setErr("读取文件失败", err.Error())
		return
	}
	// 原始字节全存（文本供 Markdown 预览，二进制供重读比对跳过重解码）
	ws.dirs.file.content = string(content)
	if binary {
		// 二进制：图片扩展名走预览，其余明确提示不支持
		if isImagePath(abs) {
			if bmp, derr := ui.DecodeBitmap(content); derr == nil {
				ws.dirs.file.kind = "image"
				ws.dirs.file.bitmap = bmp
				return
			}
		}
		ws.dirs.file.kind = "unsupported"
		return
	}
	ws.dirs.file.kind = "text"
	// 官方编辑器只读查看：按文件名选语言做语法高亮，未知扩展名为纯文本
	st := &editor.State{
		Buffer:      editor.NewBuffer(string(content)),
		ReadOnly:    true,
		LineNumbers: editor.LineNumbersOn,
	}
	st.Buffer.SetLanguage(editor.LanguageFor(abs))
	ws.dirs.file.ed = st
	if isMarkdownPath(abs) {
		ws.mdOpts.LoadImage = markdown.FileImages(dirOf(abs))
	}
	ws.jumpToFileLine()
}

// jumpToFileLine 把文件视图跳到打开请求指定的行（1 起），跳到即消费：
// 之后的 watch 重载不再把视图拽回原处。
func (ws *workspace) jumpToFileLine() {
	v := ws.dirs.file
	if v == nil || v.ed == nil || v.line <= 0 {
		return
	}
	v.ed.GoTo(editor.Pos{Line: v.line - 1})
	v.line = 0
}

// relPath 显示用：abs 去掉 root 前缀。
func relPath(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// imageExtensions 支持预览的图片扩展名（ui.DecodeBitmap 支持的格式）。
var imageExtensions = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "bmp": true,
}

// isImagePath 按扩展名判断是否可作图片预览。
func isImagePath(path string) bool {
	dot := strings.LastIndex(path, ".")
	if dot == -1 {
		return false
	}
	return imageExtensions[strings.ToLower(path[dot+1:])]
}
