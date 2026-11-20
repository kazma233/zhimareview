package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"zhimareview/internal/git"
)

// commitPageSize 是提交历史分页大小：首屏加载一页，列表滚到接近末尾再追加下一页。
const commitPageSize = 100

// historyPane 是提交历史页的状态：列表（全量/过滤/分页）、选中与详情、
// 仓库下拉。唯一写点：loadCommits/applyCommits/loadCommitDetail 等管线的
// apply、switchActiveRepo 的重置、rebuildOptions 的下拉派生、渲染帧里的
// 过滤开关与选区。sel 一律 -1 起步（坑 11）。
type historyPane struct {
	all        []git.CommitRow // 全量提交（含 merge）
	commits    []git.CommitRow // 展示的提交：按 hideMerges + query 过滤（refilterCommits）
	hideMerges bool            // 勾选：不展示 merge commit
	query      string          // 搜索词：标题 / 作者 / hash 子串
	loading    bool            // 首页加载中
	fetching   bool            // 追加页在途（防重入）
	done       bool            // 已加载完仓库全部提交
	// allLoading 是搜索触发的全量历史加载在途；allLoaded 表示 all 已含全部
	// 提交（分页到底或全量加载成功）；allTried 表示全量加载已尝试，失败后
	// 不再每帧重试
	allLoading bool
	allLoaded  bool
	allTried   bool
	// statusByRepo 是各仓库的分支跟踪状态（路径 → 分支/上游/ahead/behind）：
	// 当前仓库随提交列表加载刷新，下拉打开时并发补齐全部仓库
	statusByRepo  map[string]git.BranchStatus
	statusLoading bool
	sel           int
	list          ui.ListState
	// restore 是列表重建后要复位的提交 hash：重载（焦点刷新/fetch/pull）时
	// 记下当前选中，列表回来后按 hash 找回——提交还在就保持选中与详情，
	// 没了或被过滤掉才回到未选中。用户自己改选/改过滤时清空。
	restore       string
	detailText    string
	detailLoading bool
	// files/diffs 是选中提交补丁的逐文件官方 diff 视图状态
	files []editor.FilePatch
	diffs []editor.DiffState
	// selMerge 标记选中提交是否 merge（合并提交禁 revert）
	selMerge bool
	// gen 是列表加载世代：loadCommits 递增、追加页捕获，在途结果凭世代
	// 落地——fetch/pull 后的重载不会被旧分页结果追加污染
	gen int
	// remoteBusy 是远程操作在途标记（fetch/pull）；非空时按钮禁用，
	// 也阻止全局 fetch 与单仓库操作并发
	remoteBusy string
	split      float32
	// 仓库下拉（rebuildOptions 从模型派生；唯一消费者是历史页）。
	// repoCombo 是选中值；repoText 是输入框的编辑文本（过滤时与选中值分离，
	// 关闭下拉时回到选中值）；repoOpen 标记下拉展开，展开期间扫描重建选项
	// 不覆盖用户正在输入的文字
	repoOptions []string
	repoByLabel map[string]string
	repoCombo   string
	repoText    string
	repoOpen    bool
}

// rebuildOptions 按模型重建仓库下拉的选项与当前值（rebuildDerived 编排调用）。
func (h *historyPane) rebuildOptions(repos []git.Repo, active string) {
	h.repoOptions = make([]string, 0, len(repos))
	h.repoByLabel = map[string]string{}
	for _, repo := range repos {
		label := repoDisplayName(&repo)
		// 同名（如 group/a 与 group2/a 同名段）时退回完整 rel 保证唯一
		if _, dup := h.repoByLabel[label]; dup {
			label = repo.Rel
		}
		h.repoOptions = append(h.repoOptions, label)
		h.repoByLabel[label] = repo.Path
	}
	if active != "" {
		h.repoCombo = repoLabel(repos, active)
		// 下拉展开时可能是用户正在输入过滤词，不用选中值覆盖它
		if !h.repoOpen {
			h.repoText = h.repoCombo
		}
	}
}

// loadCommits 加载当前仓库的第一页提交（只在提交历史页触发：页面渲染与
// 切仓库都有 mainTab 守卫，工作区页不加载 git 历史）。commits 为 nil 表示
// 未加载过。
func (ws *workspace) loadCommits() {
	repo := ws.activeRepo
	if repo == "" {
		return
	}
	ws.hist.gen++
	gen := ws.hist.gen
	ws.hist.loading = true
	loadAsync(ws, func() logResult {
		page, err := git.LogCommits(repo, 0, commitPageSize)
		return logResult{page: page, err: err}
	}, func(r logResult) { ws.applyCommits(gen, repo, r.page, r.err) })
}

// logResult 是提交列表分页管线的加载结果（首页与追加页共用）。
type logResult struct {
	page []git.CommitRow
	err  error
}

// loadAllCommits 为搜索一次性加载当前仓库的全部提交：首次输入搜索词时
// 触发，之后由 refilterCommits 在本地过滤。已有页保留在界面上，全量结果
// 到达后整体替换；allTried 防止失败后每帧重试。
func (ws *workspace) loadAllCommits() {
	repo := ws.activeRepo
	if repo == "" || ws.hist.allLoading || ws.hist.loading || ws.hist.allLoaded || ws.hist.allTried {
		return
	}
	ws.hist.gen++
	gen := ws.hist.gen
	// 全量加载取代在途追加页：旧追加结果凭世代丢弃
	ws.hist.fetching = false
	ws.hist.allLoading = true
	loadAsync(ws, func() logResult {
		page, err := git.LogCommits(repo, 0, 0)
		return logResult{page: page, err: err}
	}, func(r logResult) { ws.applyAllCommits(gen, repo, r.page, r.err) })
}

// applyAllCommits 应用全量提交；期间已换仓库/重新加载时作废。
// 失败保留当前已加载页并置 allTried，错误走 setErr（弹窗）。
func (ws *workspace) applyAllCommits(gen int, repo string, commits []git.CommitRow, err error) {
	if ws.hist.gen != gen || ws.activeRepo != repo {
		return
	}
	ws.hist.allLoading = false
	ws.hist.allTried = true
	if err != nil {
		ws.setErr("搜索提交失败", err.Error())
		// 全量加载的世代前移会作废首页的状态加载，失败时补一次
		ws.loadBranchStatus(repo)
		return
	}
	ws.hist.all = commits
	ws.hist.allLoaded = true
	ws.hist.done = true
	ws.hist.loading = false
	ws.refilterCommits()
	ws.restoreSelection()
	ws.loadBranchStatus(repo)
}

// loadBranchStatus 读取当前仓库的分支跟踪状态；只接受同世代的结果。
// 读取失败按无上游处理（不打扰只读浏览）。
func (ws *workspace) loadBranchStatus(repo string) {
	gen := ws.hist.gen
	loadAsync(ws, func() git.BranchStatus {
		st, _ := git.ReadBranchStatus(repo)
		return st
	}, func(st git.BranchStatus) {
		if ws.hist.gen != gen {
			return
		}
		ws.setBranchStatus(repo, st)
	})
}

// setBranchStatus 写入单个仓库的状态（map 懒初始化）。
func (ws *workspace) setBranchStatus(repo string, st git.BranchStatus) {
	if ws.hist.statusByRepo == nil {
		ws.hist.statusByRepo = map[string]git.BranchStatus{}
	}
	ws.hist.statusByRepo[repo] = st
}

// loadAllBranchStatuses 为仓库下拉并发读取所有仓库的分支状态；打开下拉时
// 触发，已有全部条目时不重复读取。fetch/pull/刷新经 invalidateCommits
// 清空状态并前移世代，在途结果自动作废。
func (ws *workspace) loadAllBranchStatuses() {
	if ws.hist.statusLoading {
		return
	}
	need := false
	for _, repo := range ws.repos {
		if _, ok := ws.hist.statusByRepo[repo.Path]; !ok {
			need = true
			break
		}
	}
	if !need {
		return
	}
	root := ws.root
	gen := ws.hist.gen
	repos := append([]git.Repo(nil), ws.repos...)
	ws.hist.statusLoading = true
	loadAsync(ws, func() map[string]git.BranchStatus {
		return git.ReadBranchStatuses(repos)
	}, func(byPath map[string]git.BranchStatus) {
		// 世代/根目录已变时结果作废；先解除在途标记，避免被作废后卡在读取中
		ws.hist.statusLoading = false
		if ws.hist.gen != gen || ws.root != root {
			return
		}
		ws.hist.statusByRepo = byPath
	})
}

// loadMoreCommits 追加下一页：列表行构建到接近已加载末尾时触发（虚拟化列表
// 只构建可见行，滚动近尾即构建末行）。只在提交历史页、已有首页且没有在途
// 请求时动作；追加不重置选中（已有行的下标不变）。
func (ws *workspace) loadMoreCommits() {
	if ws.mainTab != 1 || ws.hist.all == nil || ws.hist.done ||
		ws.hist.loading || ws.hist.fetching || ws.hist.allLoading {
		return
	}
	// 搜索的全量加载尚未开始/成功时由它接管数据；失败后允许退回分页
	if ws.hist.query != "" && !ws.hist.allLoaded && !ws.hist.allTried {
		return
	}
	repo := ws.activeRepo
	// 偏移在发起时捕获：追加页在途期间 allCommits 可能已被应用更新
	offset := len(ws.hist.all)
	gen := ws.hist.gen
	ws.hist.fetching = true
	loadAsync(ws, func() logResult {
		page, err := git.LogCommits(repo, offset, commitPageSize)
		return logResult{page: page, err: err}
	}, func(r logResult) { ws.applyCommitPage(gen, repo, r.page, r.err) })
}

// applyCommitPage 追加一页提交（绕过 refilterCommits：追加不改变已有行下标，
// 选中保持）。世代或仓库已变说明请求被重载/切换作废，直接丢弃；同代结果
// 即使已切走 tab 也先解除在途标记，避免回到历史页后永远无法追加。
func (ws *workspace) applyCommitPage(gen int, repo string, page []git.CommitRow, err error) {
	if ws.hist.gen != gen || ws.activeRepo != repo {
		return
	}
	ws.hist.fetching = false
	if ws.mainTab != 1 {
		return
	}
	if err != nil {
		ws.setErr("读取提交历史失败", err.Error())
		ws.hist.done = true
		return
	}
	ws.hist.all = append(ws.hist.all, page...)
	for _, r := range page {
		if ws.hist.hideMerges && r.Merge {
			continue
		}
		ws.hist.commits = append(ws.hist.commits, r)
	}
	if len(page) < commitPageSize {
		ws.hist.done = true
		ws.hist.allLoaded = true
	}
	// 复位的提交可能在后续页里：追加后按 hash 找回
	ws.restoreSelection()
}

// applyCommits 应用提交列表。世代或仓库已变说明请求被重载/切换作废；
// 同代结果即使已切走 tab 也先解除 loading，回到历史页时才能重新加载。
func (ws *workspace) applyCommits(gen int, repo string, commits []git.CommitRow, err error) {
	if ws.hist.gen != gen || ws.activeRepo != repo {
		return
	}
	ws.hist.loading = false
	if ws.mainTab != 1 {
		return
	}
	if err != nil {
		ws.setErr("读取提交历史失败", err.Error())
		ws.hist.all = []git.CommitRow{}
		ws.refilterCommits()
		return
	}
	ws.hist.all = commits
	ws.refilterCommits()
	ws.hist.sel = -1
	ws.hist.detailText = ""
	// 不足一页：仓库的提交全部加载完毕
	ws.hist.done = len(commits) < commitPageSize
	ws.hist.allLoaded = ws.hist.done
	ws.restoreSelection()
	ws.loadBranchStatus(repo)
}

// refilterCommits 按 hideMerges 与搜索词从全量提交重建展示列表；过滤条件
// 变化时选中失效，回到未选中（新列表的条目下标与变化前不对应）。
func (ws *workspace) refilterCommits() {
	ws.hist.commits = nil
	query := strings.ToLower(strings.TrimSpace(ws.hist.query))
	for _, r := range ws.hist.all {
		if ws.hist.hideMerges && r.Merge {
			continue
		}
		if query != "" && !commitMatches(r, query) {
			continue
		}
		ws.hist.commits = append(ws.hist.commits, r)
	}
	ws.hist.sel = -1
	ws.hist.detailText = ""
}

// commitMatches 判断提交是否命中搜索词：message（标题）/ 作者 / 完整或
// 短哈希，忽略大小写。query 由调用方转小写。
func commitMatches(row git.CommitRow, query string) bool {
	return strings.Contains(strings.ToLower(row.Subject), query) ||
		strings.Contains(strings.ToLower(row.Author), query) ||
		strings.Contains(strings.ToLower(row.Hash), query) ||
		strings.Contains(strings.ToLower(row.ShortHash), query)
}

// invalidateCommits 使提交列表失效：清空列表/选中/详情与在途标记，
// 世代前移——旧请求的结果回来时凭世代丢弃。列表整体换掉，复位意图也清空
// （reloadCommits 自己记下来再设回）。
func (ws *workspace) invalidateCommits() {
	ws.hist.gen++
	ws.hist.restore = ""
	ws.hist.all = nil
	ws.hist.commits = nil
	ws.hist.sel = -1
	ws.hist.detailText = ""
	ws.hist.detailLoading = false
	ws.hist.files = nil
	ws.hist.diffs = nil
	ws.hist.selMerge = false
	ws.hist.loading = false
	ws.hist.fetching = false
	ws.hist.done = false
	ws.hist.allLoading = false
	ws.hist.allLoaded = false
	ws.hist.allTried = false
	ws.hist.statusByRepo = nil
	ws.hist.statusLoading = false
}

// reloadCommits 让列表失效并按当前页重载（不在历史页时等进入页面再加载）。
// 选中的提交按 hash 记下，列表回来后复位：重载不该把用户的选中弄丢。
// 已有待复位的 hash（选中的提交还没加载到）时沿用，别让下一次刷新把它忘掉。
func (ws *workspace) reloadCommits() {
	restore := ws.hist.restore
	if restore == "" && ws.hist.sel >= 0 && ws.hist.sel < len(ws.hist.commits) {
		restore = ws.hist.commits[ws.hist.sel].Hash
	}
	ws.invalidateCommits()
	ws.hist.restore = restore
	if ws.mainTab == 1 {
		ws.loadCommits()
	}
}

// restoreSelection 在列表重建后把选中复位到 reloadCommits 记下的提交：
// 还在就恢复选中并重载详情；列表已完整加载仍找不到（提交没了，或被当前
// 过滤条件排除）就保持未选中。
func (ws *workspace) restoreSelection() {
	h := ws.hist.restore
	if h == "" {
		return
	}
	for i, r := range ws.hist.commits {
		if r.Hash != h {
			continue
		}
		ws.hist.restore = ""
		ws.hist.sel = i
		ws.loadCommitDetail(r)
		return
	}
	if ws.hist.done {
		// 分页已到底还没有：提交确实不在了，不必等后续追加页
		ws.hist.restore = ""
	}
}

// fetchActiveRepo 拉取当前仓库的远程引用，成功后重载提交列表。
func (ws *workspace) fetchActiveRepo() {
	repo := ws.activeRepo
	if repo == "" || ws.hist.remoteBusy != "" || ws.fetchingAll {
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
	if repo == "" || ws.hist.remoteBusy != "" || ws.fetchingAll {
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

// loadCommitDetail 加载选中提交的补丁，按文件拆成官方 diff 视图
// （SetPatch 保留 git 的 hunk 边界与原文件行号）。
func (ws *workspace) loadCommitDetail(row git.CommitRow) {
	ws.hist.detailText = ""
	ws.hist.detailLoading = true
	repo := ws.activeRepo
	loadAsync(ws, func() detailResult {
		patch, err := git.CommitPatch(repo, row.Hash)
		merge, merr := git.IsMergeCommit(repo, row.Hash)
		return detailResult{patch: patch, err: err, merge: merr == nil && merge}
	}, func(r detailResult) { ws.applyCommitDetail(repo, row, r.patch, r.err, r.merge) })
}

// detailResult 是提交补丁管线的加载结果。
type detailResult struct {
	patch string
	err   error
	merge bool
}

// applyCommitDetail 应用提交补丁；期间已切走或选择已变化时作废。
func (ws *workspace) applyCommitDetail(repo string, row git.CommitRow, patch string, err error, merge bool) {
	if ws.mainTab != 1 || ws.activeRepo != repo {
		return
	}
	if ws.hist.sel < 0 || ws.hist.sel >= len(ws.hist.commits) || ws.hist.commits[ws.hist.sel].Hash != row.Hash {
		return // 选择已变化
	}
	ws.hist.detailLoading = false
	if err != nil {
		ws.setErr("读取提交详情失败", err.Error())
		return
	}
	ws.hist.detailText = patch
	ws.setCommitPatch(patch)
	ws.hist.selMerge = merge
}

// setCommitPatch 把整份补丁解析为逐文件的 diff 视图状态。
// 解析失败（非预期格式）按无文本改动处理。
func (ws *workspace) setCommitPatch(patch string) {
	files, err := editor.ParsePatch(patch)
	if err != nil {
		files = nil
	}
	ws.hist.files = files
	ws.hist.diffs = make([]editor.DiffState, len(files))
	for i := range files {
		d := &ws.hist.diffs[i]
		d.SetPatch(files[i])
		d.FitContent = true
		lang := editor.LanguageFor(commitFileName(files[i]))
		d.Old.SetLanguage(lang)
		d.New.SetLanguage(lang)
	}
}

// commitFileName 是补丁里一个文件的展示名（新名优先，改名显示 旧 → 新）。
func commitFileName(fp editor.FilePatch) string {
	name := fp.NewName
	if name == "" {
		name = fp.OldName
	}
	if fp.OldName != "" && fp.NewName != "" && fp.OldName != fp.NewName {
		return fp.OldName + " → " + fp.NewName
	}
	return name
}

// revertSelected 撤销选中的提交（合并提交禁用）。
func (ws *workspace) revertSelected() {
	if ws.hist.sel < 0 || ws.hist.sel >= len(ws.hist.commits) {
		return
	}
	row := ws.hist.commits[ws.hist.sel]
	ws.runWrite(func() error {
		return git.RevertCommit(ws.activeRepo, row.Hash)
	}, "revert 失败", func() {
		ws.reloadCommits()
		ws.rescan(ws.root)
	})
}

// switchActiveRepo 切换当前仓库：重置历史与选中。
// repoCombo 同步是必须的：目录模式打开文件也会切仓库，
// 下拉要跟上，否则显示的是过期的仓库名。
func (ws *workspace) switchActiveRepo(path string) {
	if path == "" || path == ws.activeRepo {
		return
	}
	ws.activeRepo = path
	ws.hist.repoCombo = repoLabel(ws.repos, path)
	ws.hist.repoText = ws.hist.repoCombo
	// 换仓库即作废旧仓库的全部在途加载与选中（世代前移）
	ws.invalidateCommits()
	if ws.mainTab == 1 {
		ws.loadCommits()
	}
}

// 提交列表列宽：固定表头与行共用同一组常量，改列宽两处一起生效。
// message+分支合并为一列 Grow 占满剩余宽度（超出省略号），作者/日期
// 固定宽度钉在行尾；信息列一旦封顶，拖宽分隔条省出的空间就会堆成行尾空白。
const (
	commitHashW   = 72
	commitAuthorW = 96
	commitDateW   = 80
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
					busy := ws.hist.remoteBusy != "" || ws.fetchingAll || ws.activeRepo == ""
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
