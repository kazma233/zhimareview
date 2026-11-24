package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// fetchState 是「全部 fetch」里单个仓库的阶段。
type fetchState int

const (
	fetchWaiting fetchState = iota
	fetchRunning
	fetchOK
	fetchFailed
)

// fetchRepoItem 是一行仓库的进度：仓库身份、阶段与完整错误正文。
type fetchRepoItem struct {
	repo  git.Repo
	state fetchState
	err   string
}

// fetchAllPane 是「全部 fetch」的进度模态状态：唯一写点是 remote.go 的
// start/markStart/markDone/applyResults。模态关掉不取消后台 fetch，
// 完成时再按模态是否仍打开决定错误落点。
type fetchAllPane struct {
	open    bool
	running bool
	items   []fetchRepoItem
	done    int
	failed  int
}

// start 重置为一次新的全量 fetch：打开模态，所有仓库先标为等待。
func (f *fetchAllPane) start(repos []git.Repo) {
	f.open = true
	f.running = true
	f.done = 0
	f.failed = 0
	f.items = make([]fetchRepoItem, len(repos))
	for i, repo := range repos {
		f.items[i] = fetchRepoItem{repo: repo}
	}
}

// markStart 把仓库从等待改为进行中；重复回调不覆盖已落地状态。
func (f *fetchAllPane) markStart(repoPath string) {
	for i := range f.items {
		if f.items[i].repo.Path == repoPath && f.items[i].state == fetchWaiting {
			f.items[i].state = fetchRunning
			return
		}
	}
}

// markDone 落地单个仓库的结果；同一仓库只计一次，防止迟到回调重复计数。
func (f *fetchAllPane) markDone(res git.FetchResult) {
	for i := range f.items {
		it := &f.items[i]
		if it.repo.Path != res.Repo.Path {
			continue
		}
		if it.state == fetchOK || it.state == fetchFailed {
			return
		}
		it.state = fetchOK
		if res.Err != nil {
			it.state = fetchFailed
			it.err = res.Err.Error()
			f.failed++
		}
		f.done++
		return
	}
}

// applyResults 用最终结果校准整张进度表：进度回调可能因队列时序缺失，
// 完成时以 FetchRepos 的返回值作为权威结果。
func (f *fetchAllPane) applyResults(results []git.FetchResult) {
	f.done = 0
	f.failed = 0
	for i := range f.items {
		f.items[i].state = fetchWaiting
		f.items[i].err = ""
	}
	for _, res := range results {
		f.markDone(res)
	}
	f.running = false
}

// errorText 是失败仓库的完整错误（复制按钮用）。
func (f *fetchAllPane) errorText() string {
	var lines []string
	for i := range f.items {
		if f.items[i].state != fetchFailed {
			continue
		}
		lines = append(lines, fetchFailureLine(&f.items[i].repo, f.items[i].err))
	}
	return strings.Join(lines, "\n")
}

// fetchFailureLine 统一错误摘要的一行：仓库展示名 + 错误文本。
func fetchFailureLine(repo *git.Repo, errText string) string {
	return repoDisplayName(repo) + " — " + errText
}

// fetchAllRepos 是顶栏「全部 fetch」：并发拉取工作区所有仓库的远程引用，
// 进度落进 fetchAll 模态。失败按仓库聚合，模态打开时不再另开错误弹窗。
func (ws *workspace) fetchAllRepos() {
	if ws.fetchAll.running {
		// 运行中再次点击 = 重新打开进度模态，不重复发起 fetch
		ws.fetchAll.open = true
		return
	}
	if ws.hist.remoteBusy != "" || len(ws.repos) == 0 {
		return
	}
	root := ws.root
	repos := append([]git.Repo(nil), ws.repos...)
	// 新动作接管错误展示：旧错误弹窗让位，结果回来后再按成败覆盖
	ws.closeErr()
	ws.fetchAll.start(repos)
	// 在进入后台段之前捕获队列：load 闭包不得读取 workspace 状态
	post := ws.post
	loadAsync(ws, func() []git.FetchResult {
		// headless（无主线程队列）不流式回调，直接同步拿最终结果
		if post == nil {
			return git.FetchRepos(repos)
		}
		return git.FetchReposProgress(repos,
			func(repo git.Repo) {
				path := repo.Path
				post(func() { ws.fetchAll.markStart(path) })
			},
			func(res git.FetchResult) {
				result := res
				post(func() { ws.fetchAll.markDone(result) })
			})
	}, func(results []git.FetchResult) {
		ws.applyFetchAll(root, results)
	})
}

// applyFetchAll 收尾全量 fetch：校准进度、反馈错误并让历史失效。
// 模态仍在时错误留在模态里（recordErr 只留档不弹窗）；用户已把模态
// 关掉时回落到错误弹窗，避免失败静默。
func (ws *workspace) applyFetchAll(root string, results []git.FetchResult) {
	if ws.root != root || ws.mode != "work" {
		ws.fetchAll.running = false
		ws.fetchAll.open = false
		return
	}
	wasOpen := ws.fetchAll.open
	ws.fetchAll.applyResults(results)

	var failed []string
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		failed = append(failed, fetchFailureLine(&r.Repo, firstLine(r.Err.Error())))
	}
	switch {
	case len(failed) == 0:
		ws.clearErr()
	default:
		title := fmt.Sprintf("fetch 失败 %d/%d", len(failed), len(results))
		body := strings.Join(failed, "\n")
		if len(failed) > 3 {
			body = strings.Join(failed[:3], "\n") + "\n等"
		}
		if wasOpen {
			ws.recordErr(title, body)
		} else {
			ws.setErr(title, body)
		}
	}
	// 让历史失效：不在历史页时等进入页面重载，在页内则立即重读
	ws.reloadCommits()
}

// fetchAllDialog 是「全部 fetch」的进度模态：总进度条 + 逐仓库状态，
// 失败仓库直接展开完整错误正文。关闭按钮/背景/Esc 只收起模态，不取消
// 后台 fetch；完成时若模态已关且失败，错误回落到错误弹窗。
func fetchAllDialog(c *ui.Context, ws *workspace) {
	f := &ws.fetchAll
	if !f.open {
		return
	}
	tk := appTokens(c)
	showDialog(c, &f.open, dialogOptions{
		title:    "全部 fetch",
		subtitle: fetchAllSubtitle(f),
		tone:     tk.Text,
		body: func() {
			total := len(f.items)
			color := tk.StatusAdd
			switch {
			case f.running:
				color = tk.Accent
			case f.failed > 0:
				color = tk.Danger
			}
			ui.Column(c).FillWidth().Gap(tk.Space(2)).Children(func() {
				ui.Row(c).FillWidth().Gap(tk.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(color).Shrink(0)
					ui.Text(c, fmt.Sprintf("已处理 %d/%d", f.done, total)).
						FontSize(tk.FontSM).TextColor(tk.Muted)
					if f.failed > 0 {
						ui.Text(c, fmt.Sprintf("%d 个失败", f.failed)).
							FontSize(tk.FontSM).TextColor(tk.Danger)
					}
					ui.Spacer(c)
					if f.running {
						ui.Spinner(c).TextColor(tk.Accent)
					}
				})
				if total > 0 {
					ui.Progress(c, float64(f.done)/float64(total)).TextColor(color)
				}
				// 仓库多或错误长时在框内滚动；卡片右侧内边距给覆盖式滚动条让位
				dialogCard(c, "fetch-all-list", 340, func() {
					ui.Column(c).FillWidth().Gap(tk.Space(2.5)).Children(func() {
						for i := range f.items {
							fetchItemView(c, tk, &f.items[i])
						}
					})
				})
			})
		},
		actions: func() {
			if f.failed > 0 {
				if ui.Button(c, "复制错误").Clicked() {
					c.WriteClipboardThen(f.errorText(), func(err error) {
						if err != nil {
							c.Toast("复制失败")
						} else {
							c.Toast("已复制 fetch 错误")
						}
					})
				}
			}
			if f.running {
				if ui.Button(c, "隐藏").Clicked() {
					f.open = false
				}
			} else if ui.PrimaryButton(c, "关闭").Clicked() {
				f.open = false
			}
		},
	})
}

// fetchAllSubtitle 是模态副标题：进行中说明并发规模，结束后收口成败。
func fetchAllSubtitle(f *fetchAllPane) string {
	switch {
	case f.running:
		return fmt.Sprintf("正在并发拉取 %d 个仓库…", len(f.items))
	case f.failed > 0:
		return fmt.Sprintf("拉取完成：%d 个仓库，%d 个失败", len(f.items), f.failed)
	default:
		return fmt.Sprintf("拉取完成：%d 个仓库", len(f.items))
	}
}

// fetchItemView 是一行仓库进度；失败时把 git 的多行提示原样展开。
func fetchItemView(c *ui.Context, tk tokens, it *fetchRepoItem) {
	label, color := "等待中", tk.Muted
	switch it.state {
	case fetchRunning:
		label, color = "fetch 中…", tk.Accent
	case fetchOK:
		label, color = "完成", tk.StatusAdd
	case fetchFailed:
		label, color = "失败", tk.Danger
	}
	ui.Column(c).FillWidth().Gap(tk.Space(1.5)).Children(func() {
		ui.Row(c).FillWidth().Gap(tk.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(color).Shrink(0)
			ui.Text(c, repoDisplayName(&it.repo)).Font(monoFont()).FontSize(tk.FontMD).
				Grow(1).SingleLine().TruncateMiddle().Tooltip(it.repo.Path)
			ui.Text(c, label).FontSize(tk.FontXS).TextColor(color).
				Background(color.Alpha(0.12)).Padding(2, 7).Radius(tk.BadgeRadius).Shrink(0)
		})
		if it.err == "" {
			return
		}
		ui.Column(c).FillWidth().Padding(tk.Space(2), tk.Space(2.5)).
			Background(tk.Danger.Alpha(0.08)).Border(1, tk.Danger.Alpha(0.18)).
			Radius(tk.BadgeRadius).Gap(2).Children(func() {
			dialogMonoLines(c, it.err, tk.FontXS, tk.Danger)
		})
	})
}

// firstLine 取错误文本首行：git 的多行提示压成单行错误摘要。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// fetchActiveRepo 拉取当前仓库的远程引用，成功后重载提交列表。
func (ws *workspace) fetchActiveRepo() {
	repo := ws.activeRepo
	if repo == "" || ws.hist.remoteBusy != "" || ws.fetchAll.running {
		return
	}
	root := ws.root
	ws.hist.remoteBusy = "fetch"
	loadAsync(ws, func() error { return git.FetchRepo(repo) },
		func(err error) { ws.applyRemoteOp(root, repo, "fetch", err) })
}

// pullActiveRepo 拉取当前仓库；默认只允许快进，rebase 为真时变基。
func (ws *workspace) pullActiveRepo(rebase bool) {
	repo := ws.activeRepo
	if repo == "" || ws.hist.remoteBusy != "" || ws.fetchAll.running {
		return
	}
	root := ws.root
	ws.hist.remoteBusy = "pull"
	loadAsync(ws, func() error { return git.PullRepo(repo, rebase) },
		func(err error) { ws.applyRemoteOp(root, repo, "pull", err) })
}

// applyRemoteOp 收尾远程操作：清除忙碌标记并反馈错误。pull 无论成败都
// 重扫工作区（冲突或变基中断也要反映真实现场），错误在重扫应用之后再落，
// 避免 applyScanState 清空错误条把 pull 失败信息冲掉；fetch 不碰工作区。
// 期间换了根目录或当前仓库则跳过后续刷新。
func (ws *workspace) applyRemoteOp(root, repo, op string, err error) {
	// 期间已换根或回选择态：结果作废，但忙碌标记必须解除
	if ws.root != root || ws.mode != "work" {
		ws.hist.remoteBusy = ""
		return
	}
	finish := func() {
		ws.hist.remoteBusy = ""
		// 重扫期间换了根目录/回选择态则不再落地
		if ws.root != root || ws.mode != "work" {
			return
		}
		if err != nil {
			ws.setErr(op+" 失败", err.Error())
		} else {
			ws.clearErr()
		}
		if err == nil || op == "pull" {
			if ws.activeRepo == repo {
				ws.reloadCommits()
			}
		}
	}
	if op == "pull" {
		ws.rescanWith(root, finish)
		return
	}
	finish()
}
