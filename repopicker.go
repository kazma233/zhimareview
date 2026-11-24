package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// repoComboView 是历史页的仓库选择器。原生 Combobox 的选项只支持单行，
// 这里用 ComboboxBase 自绘下拉：每个仓库下显示当前分支与 ahead/behind。
// 选中项一行：输入框显示仓库名（正常色），点后面的分支/upstream 是独立的
// 灰色文本；输入框编辑 repoText（关闭时同步为仓库名），展开时清空过滤。
func (ws *workspace) repoComboView(c *ui.Context, tk tokens) {
	// 关闭时先同步输入框：保证点击展开的那一帧能识别“输入框里是选中值”，
	// 从而清空输入进入过滤
	if !ws.hist.repoOpen {
		if ws.hist.repoText != ws.hist.repoCombo {
			ws.hist.repoText = ws.hist.repoCombo
		}
	}
	var cb ui.ComboboxParts
	// 字段外观挂在外层 Row；仓库名、灰色分支、箭头自左向右并列——分支文本
	// 止于箭头左侧，长内容只在各自区域内裁剪，不会画到箭头下面
	field := ui.Row(c).FillWidth().Grow(1).Height(30).PaddingEnd(8).Gap(6).
		AlignItems(ui.Center).Background(tk.Surface).Radius(tk.RowRadius)
	field.Children(func() {
		cb = ui.ComboboxBase(c.Key("repo-combo"), &ws.hist.repoText)
		cb.Input.Grow(1).MinWidth(80).Height(28).PaddingY(3).PaddingStart(8).
			Label("选择仓库").Placeholder("搜索仓库")
		// 当前仓库的分支/upstream：独立灰色文本，长分支名截断、计数不收缩
		st := ws.hist.statusByRepo[ws.activeRepo]
		if branch, track := branchStatusParts(st); branch != "" {
			ui.Row(c).MaxWidth(160).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "·").FontSize(tk.FontXS).TextColor(tk.Muted).Shrink(0)
				tip := "分支：" + branch
				if st.Upstream != "" {
					tip += "\n上游：" + st.Upstream
				}
				ui.Text(c, branch).FontSize(tk.FontXS).TextColor(tk.Muted).
					SingleLine().ClipX().Tooltip(tip)
				if track != "" {
					color := tk.Muted
					if st.Gone {
						color = tk.StatusMod
					}
					ui.Text(c, track).FontSize(tk.FontXS).TextColor(color).SingleLine().Shrink(0)
				}
			})
		}
		arrow := ui.ButtonBase(c).Label("打开仓库列表").Size(14, 14).Shrink(0).Children(func() {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				var path ui.Path
				path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).
					LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).
					LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
				p.StrokePath(&path, 1.5, tk.Muted)
			})
		})
		if arrow.Clicked() {
			cb.SetOpen(!cb.Open())
			if cb.Open() {
				cb.Input.Focus()
			}
		}
	})
	shown := ws.hist.repoOptions
	if cb.Filtering() {
		shown = matchRepoLabels(ws.hist.repoOptions, ws.hist.repoText)
	}
	cb.Popup(func(panel ui.Element) {
		// 弹层锚点是变窄的输入框；给面板一个与左栏相称的最小宽度，
		// 富选项的分支行才有空间，不被输入框宽度拖累
		popupW := max(min(ws.hist.split-16, float32(420)), float32(240))
		panel.MinWidth(popupW).Padding(4).Background(tk.Surface).Border(1, tk.Border).Radius(tk.RowRadius)
		panel.Children(func() {
			ui.Scroll(c).FillWidth().MaxHeight(260).Children(func() {
				if len(shown) == 0 {
					ui.Text(c, "没有匹配的仓库").FontSize(tk.FontSM).TextColor(tk.Muted).Padding(6, 10)
					return
				}
				for _, label := range shown {
					ws.repoOptionView(c, tk, cb, label)
				}
			})
		})
	})
	if cb.Open() {
		ws.loadAllBranchStatuses()
		// 展开即清空输入，键入直接过滤（等价于原生 Combobox 的 select-all）
		if !cb.Filtering() && ws.hist.repoText == ws.hist.repoCombo {
			ws.hist.repoText = ""
		}
	} else if ws.hist.repoText != ws.hist.repoCombo {
		ws.hist.repoText = ws.hist.repoCombo
	}
	ws.hist.repoOpen = cb.Open()
	if v, ok := cb.Chosen(); ok {
		if _, exists := ws.hist.repoByLabel[v]; exists {
			ws.hist.repoCombo = v
			ws.hist.repoText = v
			ws.switchActiveRepo(ws.hist.repoByLabel[v])
		}
	}
	// 聚焦时边框高亮，与原 Combobox 的观感一致
	if cb.Input.Focused() {
		field.Border(1, tk.Accent)
	} else {
		field.Border(1, tk.Border)
	}
}

// repoOptionView 是仓库下拉的一项：仓库名 + 当前分支/上游状态。
func (ws *workspace) repoOptionView(c *ui.Context, tk tokens, cb ui.ComboboxParts, label string) {
	item := cb.Item(label).FillWidth().Padding(5, 8).Radius(tk.BadgeRadius)
	highlighted := item.Highlighted()
	if highlighted {
		item.Background(tk.Accent).TextColor(tk.AccentText)
	}
	st := ws.hist.statusByRepo[ws.hist.repoByLabel[label]]
	item.Children(func() {
		ui.Column(c).FillWidth().Gap(1).Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).FontSize(tk.FontMD).Grow(1).SingleLine().ClipX()
				if label == ws.hist.repoCombo {
					color := tk.Muted
					if highlighted {
						color = tk.AccentText
					}
					ui.Text(c, "当前").FontSize(tk.FontXS).TextColor(color).Shrink(0)
				}
			})
			if line := repoBranchLine(st); line != "" {
				color := tk.Muted
				if st.Gone && !highlighted {
					color = tk.StatusMod
				}
				if highlighted {
					color = tk.AccentText
				}
				ui.Text(c, line).FontSize(tk.FontXS).TextColor(color).SingleLine().ClipX()
			}
		})
	})
}

// repoBranchLine 是下拉项的第二行：分支名 + ahead/behind/上游已删除。
func repoBranchLine(st git.BranchStatus) string {
	branch, track := branchStatusParts(st)
	if branch == "" {
		return ""
	}
	if track == "" {
		return branch
	}
	return branch + " " + track
}

// matchRepoLabels 是仓库下拉的过滤：忽略大小写，前缀匹配排前；空查询返回全部。
func matchRepoLabels(options []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return options
	}
	var starts, contains []string
	for _, o := range options {
		l := strings.ToLower(o)
		if strings.HasPrefix(l, q) {
			starts = append(starts, o)
		} else if strings.Contains(l, q) {
			contains = append(contains, o)
		}
	}
	return append(starts, contains...)
}
