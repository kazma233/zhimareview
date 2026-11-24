package main

import (
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"zhimareview/internal/store"
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

// loadRecent 异步读取最近目录列表；读不到就保持空，入口页不显示该区块。
// headless（post 未绑定）不读：测试直接注入 ws.recent。
func (ws *workspace) loadRecent() {
	if ws.post == nil {
		return
	}
	loadAsync(ws, func() []string {
		return store.LoadRecent(userDataDir())
	}, func(recent []string) {
		if len(recent) > 0 {
			ws.recent = recent
		}
	})
}

// chooseDir 进入工作区：换根目录、写最近列表、重新扫描、会话跟随。
// 只做状态变更与异步副作用，供 view 帧内和 Update 回调里调用——
// 帧内同步调用窗口 API（hop 主线程队列）会死锁，见 syncTitle。
func (ws *workspace) chooseDir(dir string) {
	ws.root = dir
	ws.mode = "work"
	ws.clearErr()
	ws.activeRepo = ""
	ws.app.syncTitle()
	ws.startWatch()
	ws.rescan(dir)
	ws.app.saveSession()

	go func() {
		if ws.app == nil {
			// headless 测试没有窗口，持久化行为由 store 层单测覆盖
			return
		}
		recent := store.RememberRecent(userDataDir(), dir)
		ws.post(func() { ws.recent = recent })
	}()
}

// browse 打开系统目录选择对话框；对话框同步阻塞，经管线离开主线程调用。
func (ws *workspace) browse() {
	loadAsync(ws, func() string {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:     "选择工作区根目录",
			Directory: true,
		})
		if err != nil || len(paths) == 0 {
			return ""
		}
		// 应用内路径统一 `/` 分隔：与 git 输出一致，避免两套分隔符比较失效。
		return filepath.ToSlash(paths[0])
	}, func(dir string) {
		if dir != "" {
			ws.chooseDir(dir)
		}
	})
}
