package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// fileView 是主区打开的工作区文件（只读查看）。
type fileView struct {
	abs     string
	loading bool
	kind    string // text / image / unsupported
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
			if ui.Button(c, "复制路径").Clicked() {
				c.WriteClipboard(v.abs)
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
