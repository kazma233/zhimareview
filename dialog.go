package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// dialogOptions 是应用内模态的统一骨架：标题/副标题、正文、底部动作。
// 具体内容自行决定内部布局；间距、分隔线与默认宽度由 showDialog 统一。
type dialogOptions struct {
	title    string
	subtitle string
	tone     ui.Color // 标题色；调用方显式传 Text / Danger
	width    float32  // 0=默认 640
	body     func()
	actions  func()
}

// showDialog 在 MyGo Modal 的默认面板内搭出标题区/正文区/动作区。
// 面板自带 20px 内边距、圆角与阴影；这里只补分区、分隔线和默认宽度，
// 避免每个弹窗各写一套间距。
func showDialog(c *ui.Context, open *bool, o dialogOptions) {
	tk := appTokens(c)
	width := o.width
	if width == 0 {
		width = 640
	}
	ui.Modal(c, open, func() {
		ui.Column(c).Width(width).Gap(tk.Space(3)).Children(func() {
			ui.Column(c).FillWidth().Gap(tk.Space(1)).Children(func() {
				ui.Text(c, o.title).Bold().FontSize(tk.FontLG).TextColor(o.tone)
				if o.subtitle != "" {
					ui.Text(c, o.subtitle).FontSize(tk.FontSM).TextColor(tk.Muted)
				}
			})
			if o.body != nil {
				ui.Divider(c)
				o.body()
			}
			if o.actions != nil {
				ui.Divider(c)
				ui.Row(c).FillWidth().Gap(tk.Space(2)).Justify(ui.End).Children(o.actions)
			}
		})
	})
}

// dialogCard 是模态正文的滚动卡片：Surface 底、圆角边框、内边距；
// 右侧留 14px 给覆盖式滚动条，正文不会画到滚动条下面。
func dialogCard(c *ui.Context, key string, maxHeight float32, body func()) {
	tk := appTokens(c)
	ui.Scroll(c.Key(key)).FillWidth().MaxHeight(maxHeight).
		Padding(tk.Space(3), tk.Space(3.5)).
		Background(tk.Surface).Radius(tk.RowRadius).Border(1, tk.Border).
		Children(body)
}

// dialogMonoLines 逐行渲染 git 原始提示，保留换行与缩进。
func dialogMonoLines(c *ui.Context, text string, size float32, color ui.Color) {
	for _, line := range strings.Split(text, "\n") {
		ui.Text(c, line).Font(monoFont()).FontSize(size).TextColor(color).FillWidth()
	}
}
