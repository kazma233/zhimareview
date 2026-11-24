package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"zhimareview/internal/git"
)

// 提交列表列宽：固定表头与行共用同一组常量，改列宽两处一起生效。
// message+分支合并为一列 Grow 占满剩余宽度（超出省略号），作者/日期
// 固定宽度钉在行尾；信息列一旦封顶，拖宽分隔条省出的空间就会堆成行尾空白。
const (
	commitHashW   = 72
	commitAuthorW = 96
	commitDateW   = 80
)

// historyView 是提交历史：左列（仓库切换下拉 + 固定表头 + 提交列表）+
// 右侧逐文件 diff（从页面顶端占满高度）。
func (ws *workspace) historyView(c *ui.Context) {
	tk := appTokens(c)
	// 历史未加载时触发一次（切换 tab 或切仓库后）；工作区页不会走到这里
	if ws.hist.all == nil && !ws.hist.loading && ws.activeRepo != "" {
		ws.loadCommits()
	}
	// 搜索需要完整历史：首次输入搜索词后一次性加载当前仓库全部提交，
	// 之后在本地过滤；失败由 allTried 阻止每帧重试
	if ws.hist.query != "" && !ws.hist.allLoaded && !ws.hist.allTried &&
		!ws.hist.allLoading && !ws.hist.loading && ws.activeRepo != "" {
		ws.loadAllCommits()
	}

	// 占「剩余高度」用 Grow 而非 Fill（坑 14）：父级是顶栏之下的整页，
	// Fill 会以窗口全高为基数叠加顶栏，min-content 不许收缩，页面底部
	// 越出窗口——提交列表/补丁底部真机不可达
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Split(c, &ws.hist.split, func() {
			// 左列：仓库切换下拉（只在列表列宽内，不占整页顶部）+ 固定表头 + 列表
			ui.Column(c).Fill().Children(func() {
				// 仓库选择行：仓库下拉（选中项一行：仓库名 · 分支/upstream）
				// + 远程操作按钮。全局 fetch 或本仓库远程操作在途时禁用按钮，
				// 避免同一仓库的并发 git 写操作。
				// 顶 6 呼吸空隙（工作区分段行同款，贴着顶栏太挤），左 10 /
				// 右 12 同 diff 列表行；内容区保持 30，控件观感不变只挪位置
				ui.Row(c).FillWidth().Height(36).Padding(6, 12, 0, 10).Gap(10).AlignItems(ui.Center).Children(func() {
					// 仓库切换下拉放在历史页：activeRepo 决定历史读哪个仓库，
					// 工作区的改动树是跨仓库聚合，切仓库在那里没有效果
					ws.repoComboView(c, tk)
					busy := ws.hist.remoteBusy != "" || ws.fetchAll.running || ws.activeRepo == ""
					// 文案不随状态改：进行中靠置灰表达（按钮文案会跳变，
					// 读界面时按钮位置与宽度都在动）
					if ui.Button(c, "fetch").Disabled(busy).Clicked() {
						ws.fetchActiveRepo()
					}
					// pull 分裂按钮：外层一个边框容器（一体观感），内含两个可点区：
					// 主体点按默认快进 pull；右侧窄 caret 弹菜单选 --rebase 变基。
					// 容器只画一道边框、区间一条分隔线，两半 hover 各自填色——
					// 两个独立按钮各画边框会显得是两个控件（实抓）。
					// busy 时整体禁用，避免同一仓库并发 git 写
					ui.Row(c).Border(1, tk.Border).Radius(6).Background(tk.Surface).
						Gap(0).Padding(1).Shrink(0).Children(func() {
						main := ui.ButtonBase(c).Label("pull").Disabled(busy).
							PaddingX(10).Height(24)
						if main.Hovered() {
							main.Background(tk.SurfaceHover)
						}
						main.Children(func() {
							ui.Text(c, "pull").FontSize(tk.FontMD)
						})
						if main.Clicked() {
							ws.pullActiveRepo(false)
						}
						// 两半之间的分隔线
						ui.Box(c).Width(1).Height(16).Background(tk.Border).Shrink(0)
						// caret 窄：宽度只够一个三角，点击弹变基菜单
						caret := ui.ButtonBase(c).Label("pull 变体").Disabled(busy).
							Width(18).Height(24).
							Tooltip("--rebase：变基 pull；直接点 pull 只允许快进")
						if caret.Hovered() {
							caret.Background(tk.SurfaceHover)
						}
						caret.Children(func() {
							ui.Text(c, "▾").FontSize(tk.FontSM).TextColor(tk.Muted)
						})
						// 菜单项 Chosen 在菜单关闭后的帧里回调，经管线离开主
						// 线程，无帧内变更风险
						caret.Menu(func(m *ui.Menu) {
							if m.Item("--rebase").Chosen() {
								ws.pullActiveRepo(true)
							}
						})
					})
				})
				// 工具行：搜索 + 隐藏 merge。搜索框 Grow 吃掉剩余宽度，
				// 过滤开关钉在行尾；上下各 6 呼吸空隙，与仓库行、表头不贴死
				ui.Row(c).FillWidth().Height(44).Padding(6, 12, 6, 10).Gap(10).AlignItems(ui.Center).Children(func() {
					if ui.SearchField(c.Key("commit-search"), &ws.hist.query).
						Grow(1).
						Placeholder("搜索标题 / 作者 / hash").
						Label("搜索提交").
						Changed() {
						// 新搜索词允许重新尝试全量加载（上次失败后不永久卡住）
						ws.hist.allTried = false
						// 用户改了过滤条件：待复位的选中作废，别在过滤结果里蹦回来
						ws.hist.restore = ""
						ws.refilterCommits()
					}
					switch {
					case ws.hist.allLoading:
						ui.Text(c, "搜索中…").FontSize(tk.FontXS).TextColor(tk.Muted)
					case ws.hist.query != "":
						ui.Textf(c, "%d 条", len(ws.hist.commits)).FontSize(tk.FontXS).TextColor(tk.Muted)
					}
					// 勾选隐藏 merge commit：Checkbox 在构建时翻转布尔，前后比对触发过滤
					prev := ws.hist.hideMerges
					ui.Checkbox(c, &ws.hist.hideMerges, "隐藏 merge")
					if ws.hist.hideMerges != prev {
						ws.hist.restore = ""
						ws.refilterCommits()
					}
				})
				commitListHeader(c, tk)
				switch {
				case ws.hist.loading && ws.hist.commits == nil:
					emptyState(c, "加载中…", "")
				case ws.hist.allLoading && len(ws.hist.commits) == 0:
					emptyState(c, "搜索中…", "正在加载完整提交历史")
				case ws.hist.commits == nil || len(ws.hist.commits) == 0:
					if ws.hist.query != "" {
						emptyState(c, "没有匹配的提交", "按标题 / 作者 / hash 过滤")
					} else {
						emptyState(c, "没有提交", "")
					}
				default:
					ws.hist.list.Selected = &ws.hist.sel
					n := len(ws.hist.commits)
					lst := ui.List(c, &ws.hist.list, n, func(i int) {
						// 虚拟化列表只构建可见行：末行被构建说明已滚到接近
						// 末尾，追加下一页（loadMoreCommits 自带防重入与完毕守卫）
						if i == n-1 {
							ws.loadMoreCommits()
						}
						ws.commitRowView(c, ws.hist.commits[i])
					}).FillWidth().Grow(1)
					if lst.Changed() && ws.hist.sel >= 0 && ws.hist.sel < n {
						// 用户自己改选了：待复位的旧选中作废
						ws.hist.restore = ""
						ws.loadCommitDetail(ws.hist.commits[ws.hist.sel])
					}
				}
			})
		}, func() {
			// 右侧：提交详情 + 逐文件补丁，从页面顶端占满高度
			ui.Column(c).Fill().Children(func() {
				if ws.hist.sel < 0 || ws.hist.sel >= len(ws.hist.commits) {
					ui.Column(c).Fill().Center().Gap(4).Children(func() {
						ui.Text(c, "提交详情").FontSize(tk.FontLG).FontWeight(600)
						ui.Text(c, "从左侧选择一次提交查看补丁").TextColor(tk.Muted).FontSize(tk.FontSM)
					})
					return
				}
				row := ws.hist.commits[ws.hist.sel]
				ui.Row(c).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						// ClipX 放弃 min-content 地板（坑 14 族）：长标题收缩截断，
						// 不把右侧复制/revert 按钮挤出 pane
						ui.Text(c, row.Subject).FontSize(tk.FontLG).FontWeight(600).SingleLine().ClipX()
						ui.Textf(c, "%s · %s · %s", row.ShortHash, row.Author, row.Date).FontSize(tk.FontXS).TextColor(tk.Muted).SingleLine().ClipX()
					})
					// 布局分段与主 diff 共用偏好（逐文件小卡片默认吃统一
					// 布局的窄幅优势）
					layoutSwitch(c, ws)
					if ui.Button(c, "复制 hash").Clicked() {
						c.WriteClipboard(row.Hash)
					}
					// 文案固定：合并提交不可 revert 时置灰即可，理由放悬停
					// 提示（文案随状态改会让按钮宽度跳变）
					revertBtn := ui.Button(c, "revert").Disabled(ws.hist.selMerge)
					if ws.hist.selMerge {
						revertBtn = revertBtn.Tooltip("合并提交不可 revert")
					}
					if revertBtn.Clicked() {
						ws.revertSelected()
					}
				})
				ui.Divider(c)

				switch {
				case ws.hist.detailLoading:
					emptyState(c, "加载中…", "")
				case ws.hist.detailText == "":
					emptyState(c, "没有文件改动", "")
				case len(ws.hist.files) == 0:
					// 补丁里没有可显示的文本文件：二进制或纯属性变化
					emptyState(c, "没有可显示的文本改动", "二进制文件或纯属性变化不带文本补丁")
				default:
					ui.Scroll(c).Fill().Children(func() {
						ui.Column(c).FillWidth().Children(func() {
							for i := range ws.hist.files {
								fp := &ws.hist.files[i]
								d := &ws.hist.diffs[i]
								ui.Row(c).FillWidth().Height(tk.RowH).PaddingX(8).Gap(8).AlignItems(ui.Center).
									Background(tk.Surface).Children(func() {
									ui.Text(c, commitFileName(*fp)).Font(monoFont()).FontSize(tk.FontSM).
										FontWeight(600).Grow(1).SingleLine().TruncateMiddle()
									if fp.Binary {
										ui.Text(c, "二进制文件").FontSize(tk.FontXS).TextColor(tk.Muted)
									}
								})
								if !fp.Binary {
									// 布局与主 diff 共用偏好，渲染帧统一落值
									d.Layout = diffLayout(ws.diffSeg)
									editor.DiffView(c, d).FillWidth()
								}
							}
						})
					})
				}
			})
		}).FillWidth().Grow(1)
	})
}

// commitListHeader 是提交列表的固定表头：不随列表滚动，列宽与行共用常量
// （信息列占满剩余宽度，作者/日期钉在行尾，与行保持列对齐）。
func commitListHeader(c *ui.Context, tk tokens) {
	ui.Row(c).FillWidth().Height(22).PaddingX(8).Gap(6).AlignItems(ui.Center).
		Background(tk.Surface).Children(func() {
		ui.Text(c, "提交").FontSize(tk.FontXS).TextColor(tk.Muted).Width(commitHashW)
		ui.Text(c, "提交信息").FontSize(tk.FontXS).TextColor(tk.Muted).Grow(1)
		ui.Text(c, "作者").FontSize(tk.FontXS).TextColor(tk.Muted).Width(commitAuthorW)
		ui.Text(c, "日期").FontSize(tk.FontXS).TextColor(tk.Muted).Width(commitDateW)
	})
}

// branchStatusParts 把当前分支拆成名称与跟踪标记两部分：ahead/behind 计数
// 单独渲染，长分支名收缩截断时计数仍然可见。上游已删除时标记代替计数。
func branchStatusParts(st git.BranchStatus) (branch, track string) {
	if st.Branch == "" {
		return "", ""
	}
	if st.Gone {
		return st.Branch, "上游已删除"
	}
	var parts []string
	if st.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", st.Ahead))
	}
	if st.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", st.Behind))
	}
	return st.Branch, strings.Join(parts, " ")
}

// commitRowView 渲染提交列表一行：短哈希、信息列（message+分支徽标合并，
// 占满哈希与作者之间的剩余宽度，超出省略号；hover 出完整 message 与全部分支）、
// 作者与日期钉在行尾。列宽与固定表头（commitListHeader）共用常量对齐。
func (ws *workspace) commitRowView(c *ui.Context, row git.CommitRow) {
	tk := appTokens(c)
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, row.ShortHash).Font(monoFont()).FontSize(tk.FontSM).TextColor(tk.Accent).
			Width(commitHashW).SingleLine().ClipX()
		msg := ui.Row(c).Grow(1).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, row.Subject).FontSize(tk.FontMD).SingleLine().ClipX()
			shown := 0
			for _, ref := range commitRefs(row) {
				if shown >= 2 {
					break
				}
				badge(c, ref)
				shown++
			}
		})
		// hover 提示层：完整 message（可换行）+ 全部分支；Tooltip 同时把这段
		// 文本挂到无障碍 Description
		tip := row.Subject
		if refs := commitRefs(row); len(refs) > 0 {
			tip += "\n" + strings.Join(refs, "  ")
		}
		msg.Tooltip(tip)
		// 作者/日期钉在行尾：短 message 的空隙留在消息与作者之间（标准
		// git 列表布局），拖宽分隔条不会再把空白堆到行尾；列宽恒定，纵向对齐
		ui.Text(c, row.Author).FontSize(tk.FontXS).TextColor(tk.Muted).
			Width(commitAuthorW).SingleLine().ClipX()
		ui.Text(c, row.Date).FontSize(tk.FontXS).TextColor(tk.Muted).
			Width(commitDateW).SingleLine().ClipX()
	})
}

// commitRefs 解析提交的引用列表（去空白）。
func commitRefs(row git.CommitRow) []string {
	var out []string
	for _, ref := range strings.Split(row.Refs, ",") {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, ref)
		}
	}
	return out
}
