package main

import (
	"slices"
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

// reloadCommits 是外部 git 操作后的历史重读（focus/切 tab/顶栏刷新/pull/
// fetch/revert 收尾都会走到）。按「重读-比对-落地」：先读首页再与当前列表
// 比对，历史没变整体不动（不置 loading、不清选中与详情、不丢翻页与搜索
// 状态），变了才失效重置。不在历史页也重读——为了回来时列表即时不闪。
// 首页比对足以代表整个历史：提交 hash 含父 hash 链，历史任何改动都会改到
// 顶端首页。
func (ws *workspace) reloadCommits() {
	repo := ws.activeRepo
	if repo == "" {
		return
	}
	gen := ws.hist.gen
	loadAsync(ws, func() logResult {
		page, err := git.LogCommits(repo, 0, commitPageSize)
		return logResult{page: page, err: err}
	}, func(r logResult) { ws.applyReloadedCommits(gen, repo, r.page, r.err) })
}

// applyReloadedCommits 应用重读结果：与当前已加载列表的前缀一致（历史未变）
// 就整体丢弃；变了才按重载语义重置落地，用已读到的首页直接应用（不再二次
// 读取）。重读失败保留现有列表只报错——读取失败不是内容变化，不该清掉用户
// 正在看的东西。
func (ws *workspace) applyReloadedCommits(gen int, repo string, page []git.CommitRow, err error) {
	if ws.hist.gen != gen || ws.activeRepo != repo {
		return
	}
	if err != nil {
		ws.setErr("读取提交历史失败", err.Error())
		return
	}
	if n := len(page); n <= len(ws.hist.all) && slices.Equal(page, ws.hist.all[:n]) {
		return
	}
	restore := ws.hist.restore
	if restore == "" && ws.hist.sel >= 0 && ws.hist.sel < len(ws.hist.commits) {
		restore = ws.hist.commits[ws.hist.sel].Hash
	}
	ws.invalidateCommits()
	ws.hist.restore = restore
	ws.applyCommits(ws.hist.gen, repo, page, nil)
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
