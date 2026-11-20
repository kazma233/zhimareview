package main

import (
	"github.com/egoist/mygo/ui"
)

// pickView 是入口选择态：选目录按钮 + 最近目录列表（有序、超出高度滚动）。
func (ws *workspace) pickView(c *ui.Context) {
	tk := appTokens(c)
	// 交叉轴居中让标题与按钮水平居中；主轴从顶开始，剩余高度让给列表区
	ui.Column(c).Fill().AlignItems(ui.Center).Gap(12).Padding(24).Children(func() {
		ui.Text(c, "zhimareview").FontSize(tk.FontXL).Bold()
		ui.Text(c, "选择一个目录，扫描其中所有 git 仓库").TextColor(tk.Muted)
		if ui.PrimaryButton(c, "选择目录…").Clicked() {
			ws.browse()
		}

		if len(ws.recent) > 0 {
			// 列表区固定宽、Grow 占剩余高度：行数超出视口在 Scroll 里滚动，
			// 上限（50 条）由 store 层保证
			ui.Column(c).Width(640).Grow(1).PaddingY(12).Gap(6).Children(func() {
				ui.Text(c, "最近使用").FontSize(tk.FontSM).FontWeight(600).TextColor(tk.Muted)
				ui.Scroll(c.Key("recent-scroll")).Fill().Children(func() {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						for i, dir := range ws.recent {
							row := ui.Button(c, "").FillWidth().Padding(8, 12).Radius(8).Background(tk.Surface).Border(1, tk.Border)
							row.Children(func() {
								// 有序列表：行首序号右对齐固定宽，两位数（≤50）也对齐
								ui.Textf(c, "%d", i+1).Font(monoFont()).FontSize(tk.FontSM).
									TextColor(tk.Muted).Width(24).TextAlign(ui.End)
								ui.Text(c, dir).Font(monoFont()).FontSize(tk.FontMD).
									SingleLine().ClipX().Grow(1)
							})
							if row.Clicked() {
								ws.chooseDir(dir)
							}
						}
					})
				})
			})
		}

	})
}
