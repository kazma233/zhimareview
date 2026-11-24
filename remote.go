package main

import (
	"fmt"
	"strings"

	"zhimareview/internal/git"
)

// fetchAllRepos 是顶栏「全部 fetch」：并发拉取工作区所有仓库的远程引用。
// 失败按仓库聚合反馈（最多列 3 个）；完成后历史失效（在历史页则立即重载）。
func (ws *workspace) fetchAllRepos() {
	if ws.fetchingAll || ws.hist.remoteBusy != "" || len(ws.repos) == 0 {
		return
	}
	root := ws.root
	repos := append([]git.Repo(nil), ws.repos...)
	ws.fetchingAll = true
	loadAsync(ws, func() []git.FetchResult { return git.FetchRepos(repos) },
		func(results []git.FetchResult) {
			ws.fetchingAll = false
			// 期间换了根目录或回到选择态：旧工作区的结果不落进新工作区
			if ws.root != root || ws.mode != "work" {
				return
			}
			var failed []string
			for _, r := range results {
				if r.Err == nil {
					continue
				}
				failed = append(failed, repoDisplayName(&r.Repo)+" — "+firstLine(r.Err.Error()))
			}
			switch {
			case len(failed) == 0:
				ws.clearErr()
			case len(failed) <= 3:
				ws.setErr(fmt.Sprintf("fetch 失败 %d/%d", len(failed), len(results)), strings.Join(failed, "\n"))
			default:
				ws.setErr(fmt.Sprintf("fetch 失败 %d/%d", len(failed), len(results)), strings.Join(failed[:3], "\n")+"\n等")
			}
			// 让历史失效：不在历史页时等进入页面重载，在页内则立即重读
			ws.reloadCommits()
		})
}

// firstLine 取错误文本首行：git 的多行提示压进单行错误条。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
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
