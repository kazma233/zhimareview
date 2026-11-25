package main

import (
	"slices"
	"strings"
	"time"

	"zhimareview/internal/git"
)

// rescan 扫描根目录下的仓库并采集改动，完成后应用结果。
// 期间换目录或回到选择态时结果作废（applyScanState 守卫）。
func (ws *workspace) rescan(dir string) {
	ws.rescanWith(dir, nil)
}

// rescanWith 是 rescan 的带完成回调版本：扫描结果应用之后（即使被守卫
// 丢弃）在主线程执行 done，供调用方在 applyScanState 清空错误条之后再
// 落自己的错误信息。
func (ws *workspace) rescanWith(dir string, done func()) {
	if dir == "" {
		return
	}
	ws.scanning = true
	// 扫描可由对话框回调触发（不在渲染帧内），置旗标后主动请求重绘
	ws.app.invalidate()
	loadAsync(ws, func() scanResult {
		found := git.FindRepos(dir)
		r := git.CollectChangesChecked(found)
		return scanResult{found: found, changes: r.Changes, failed: r.Failed}
	}, func(r scanResult) {
		// 读取失败的仓库沿用上一版条目：失败不等于改动消失
		ws.applyScanState(dir, r.found, carriedChanges(ws.changes, r.found, r.changes, r.failed))
		if done != nil {
			done()
		}
	})
}

// scanResult 是 rescan 管线的加载结果（仓库集合 + 全量改动 + 采集失败的
// 仓库：路径 → 错误）。
type scanResult struct {
	found   []git.Repo
	changes []git.Change
	failed  map[string]error
}

// carriedChanges 合并一次扫描结果：读取失败的仓库沿用上一版模型里的
// 条目——失败不等于没有改动。按 found 的仓库顺序输出；失败仓库已不在
// 扫描结果里（目录删除）时旧条目一并丢弃。
func carriedChanges(previous []git.Change, found []git.Repo, fresh []git.Change, failed map[string]error) []git.Change {
	if len(failed) == 0 {
		return fresh
	}
	byRepo := make(map[string][]git.Change, len(found))
	for _, c := range fresh {
		byRepo[c.RepoPath] = append(byRepo[c.RepoPath], c)
	}
	for _, c := range previous {
		if _, bad := failed[c.RepoPath]; bad {
			byRepo[c.RepoPath] = append(byRepo[c.RepoPath], c)
		}
	}
	out := make([]git.Change, 0, len(fresh)+len(previous))
	for _, r := range found {
		out = append(out, byRepo[r.Path]...)
	}
	return out
}

// applyScanState 应用扫描结果；root 已变或已回选择态时丢弃。仓库与改动都
// 和当前一致（例行重扫没扫出新东西）时只清过程状态，不动数据与派生——
// 重读-比对-落地，改动树/目录行的整表重建对用户是纯闪烁。
func (ws *workspace) applyScanState(dir string, found []git.Repo, changes []git.Change) {
	if ws.root != dir || ws.mode != "work" {
		return
	}
	changed := !slices.Equal(ws.repos, found) || !slices.Equal(ws.changes, changes)
	if changed {
		ws.repos = found
		ws.changes = changes
	}
	ws.scanning = false
	ws.clearErr()
	// 根目录变化时目录模式状态重置；改动树显式收起的目录按条目 id 记录
	//（tree.collapsed），跨扫描天然保持，无需重置
	if ws.root != ws.dirs.rootLoaded {
		ws.dirs.cache = map[string][]fsEntry{}
		ws.dirs.loading = map[string]bool{}
		ws.dirs.expanded = map[string]bool{}
		ws.dirs.rootLoaded = ws.root
	}
	// 默认仓库落在第一个有改动的仓库，其次第一个仓库
	if ws.activeRepo == "" || repoAt(ws.repos, ws.activeRepo) == nil {
		ws.activeRepo = firstRepoWithChanges(found, changes)
	}
	if changed {
		ws.applyChangedModel()
	}
}

// applyChangedModel 是改动模型变更后的统一收尾：先重建派生（reconcile
// 的目录入口回落要按新索引判断文件还有没有改动），再让打开中的 diff
// 跟随模型，最后重建侧栏行。扫描与 watch 合并两条写入路径共用。
func (ws *workspace) applyChangedModel() {
	ws.rebuildDerived()
	ws.reconcileOpenDiff()
	ws.dirs.rebuildRows(ws.root, ws.changes)
}

// rebuildDerived 重建派生数据：模型索引（changeByAbs）与各视图的派生
// （改动树/列表布局 tree、历史页仓库下拉 hist）。数据（repos/changes/
// activeRepo）归模型，派生归分区，本函数只做编排——从模型取数喂给
// 各纯输入派生函数。
func (ws *workspace) rebuildDerived() {
	ws.changeByAbs = buildChangeByAbs(ws.changes)
	ws.tree.rebuild(ws.root, ws.repos, ws.changes)
	ws.hist.rebuildOptions(ws.repos, ws.activeRepo)
}

// buildChangeByAbs 建文件改动索引（abs = RepoPath/Path → 改动）。同文件
// 多分区（如暂存后又修改）只留一份：工作区侧优先（unstaged > staged >
// 其他）——「查看 diff」入口先看工作区状态。指针指向 changes 切片元素，
// 索引随每次 changes 更新整体重建，指针不跨更新存活。
func buildChangeByAbs(changes []git.Change) map[string]*git.Change {
	sectionRank := func(section string) int {
		switch section {
		case "unstaged":
			return 3
		case "staged":
			return 2
		default:
			return 1
		}
	}
	idx := make(map[string]*git.Change, len(changes))
	for i := range changes {
		c := &changes[i]
		abs := changeAbs(*c)
		if best, ok := idx[abs]; ok && sectionRank(c.Section) <= sectionRank(best.Section) {
			continue
		}
		idx[abs] = c
	}
	return idx
}

// changeAbs 是改动的文件绝对路径语法（RepoPath/Path，应用内统一 / 分隔）。
func changeAbs(c git.Change) string {
	return c.RepoPath + "/" + c.Path
}

// repoAt 返回 path 对应的仓库（分区派生是纯输入，经参数拿模型）。
func repoAt(repos []git.Repo, path string) *git.Repo {
	for i := range repos {
		if repos[i].Path == path {
			return &repos[i]
		}
	}
	return nil
}

// repoLabel 是仓库在下拉与提交框里的展示名。
func repoLabel(repos []git.Repo, path string) string {
	return repoDisplayName(repoAt(repos, path))
}

// repoDisplayName 的契约：根仓库（rel 为空）用名字，其余用 rel。
func repoDisplayName(repo *git.Repo) string {
	if repo == nil {
		return ""
	}
	if repo.Rel == "" {
		return repo.Name
	}
	return repo.Rel
}

// firstRepoWithChanges 挑第一个有改动的仓库，其次第一个仓库。
func firstRepoWithChanges(found []git.Repo, changes []git.Change) string {
	changed := map[string]bool{}
	for _, c := range changes {
		changed[c.RepoPath] = true
	}
	for _, repo := range found {
		if changed[repo.Path] {
			return repo.Path
		}
	}
	if len(found) > 0 {
		return found[0].Path
	}
	return ""
}

// refresh 是顶栏刷新按钮：重扫当前根目录，并让提交历史失效（在历史页
// 立即重读，不在则等进入页面再加载）。
func (ws *workspace) refresh() {
	ws.rescan(ws.root)
	ws.reloadCommits()
}

// refreshThrottle 是「切 tab / 窗口获得焦点」重扫的节流间隔：这两个动作
// 常连着发生（切回应用就切 tab），不节流会连着跑两遍 git status。
const refreshThrottle = 2 * time.Second

// refreshOnDemand 是切 tab / 窗口获得焦点触发的重扫。外部 git 操作
// （commit / add / stash / branch / tag）只改 .git 内部状态——被监听的
// 路径（.git 被 SkipDir 排除）一个字节都没动，watch 收不到事件，界面
// 不会自己更新。这类操作多在应用之外发生（终端 / IDE），用户切回来时
// 刷新一次即可对上。
func (ws *workspace) refreshOnDemand() {
	if ws.mode != "work" || ws.root == "" {
		return
	}
	if time.Since(ws.lastRefresh) < refreshThrottle {
		return
	}
	ws.lastRefresh = time.Now()
	ws.refresh()
}

// changeForAbs 找文件对应的改动（abs = RepoPath/Path）；无改动返回 nil
// （走只读查看）。查表：多分区的取舍（工作区侧优先）在索引构建时定死，
// 索引随扫描/合并经 rebuildDerived 重建（buildChangeByAbs）。
func (ws *workspace) changeForAbs(abs string) *git.Change {
	return ws.changeByAbs[abs]
}

// repoForAbs 找覆盖 abs 的最深仓库。
func (ws *workspace) repoForAbs(abs string) string {
	best := ""
	for _, r := range ws.repos {
		if abs == r.Path || strings.HasPrefix(abs, r.Path+"/") {
			if len(r.Path) > len(best) {
				best = r.Path
			}
		}
	}
	return best
}
