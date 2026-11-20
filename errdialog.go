package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// errInfo 是错误弹窗的内容：title 是短分类（"pull 失败"），body 是原始
// 细节（git 的多行提示原样保留）。两者分开：标题给对话框，正文给复制。
type errInfo struct {
	title string
	body  string
}

// setErr 记录错误并弹出对话框。错误归 tab 所有：切到别的 tab 不弹，
// 切回来时弹（弹窗状态存在 tab 上）。
func (ws *workspace) setErr(title, body string) {
	ws.err = &errInfo{title: title, body: body}
	ws.errOpen = true
	// 未读：tab 上的红点标记着它，等用户看过弹窗才消失
	ws.errSeen = false
}

// clearErr 清掉错误（成功路径）：内容与弹窗一起消失。
func (ws *workspace) clearErr() {
	ws.err = nil
	ws.errOpen = false
	ws.errSeen = true
}

// closeErr 关闭弹窗并标记错误已读：红点消失，错误内容仍留在 tab 上
// （切回来不再弹，但可从 tab 的悬停提示看到错误标题）。
func (ws *workspace) closeErr() {
	ws.errOpen = false
	ws.errSeen = true
}

// errText 是错误的完整文本（标题 + 正文），复制与断言都用它。
func (ws *workspace) errText() string {
	if ws.err == nil {
		return ""
	}
	if ws.err.title == "" {
		return ws.err.body
	}
	return ws.err.title + "：" + ws.err.body
}

// errDialog 是错误弹窗：标题 + 可滚动的等宽正文 + 复制/关闭按钮。
// 覆盖在整窗之上（Modal 的背景铺满窗口），正文超长时内部滚动。
func errDialog(c *ui.Context, ws *workspace) {
	if ws.err == nil {
		return
	}
	if !ws.errOpen {
		// 已被关掉（Esc / 点背景 / 切走 tab）：用户看过，红点消失
		ws.errSeen = true
		return
	}
	tk := appTokens(c)
	ui.Modal(c, &ws.errOpen, func() {
		ui.Column(c).Width(560).Gap(10).Children(func() {
			ui.Text(c, ws.err.title).Bold().FontSize(tk.FontLG).TextColor(tk.Danger)
			// 正文：逐行渲染（保留 git 提示的换行与缩进），超长在框内滚动
			ui.Scroll(c.Key("err-body")).MaxHeight(320).Children(func() {
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					for _, line := range strings.Split(ws.err.body, "\n") {
						ui.Text(c, line).Font(monoFont()).FontSize(tk.FontMD).FillWidth()
					}
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "复制").Clicked() {
					// 写入结果异步回来（系统剪贴板可能被别的应用占用），
					// 用 Toast 反馈成败
					c.WriteClipboardThen(ws.errText(), func(err error) {
						if err != nil {
							c.Toast("复制失败")
						} else {
							c.Toast("已复制错误信息")
						}
					})
				}
				if ui.PrimaryButton(c, "关闭").Clicked() {
					ws.closeErr()
				}
			})
		})
	})
}
