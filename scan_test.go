package main

import (
	"testing"

	"zhimareview/internal/git"
)

// 扫描结果应用：默认仓库落在第一个有改动的仓库；换目录后旧结果作废。
func TestApplyScanState(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	repos := []git.Repo{
		{Path: "D:/repos/demo", Name: "demo", Rel: "demo"},
		{Path: "D:/repos/clean", Name: "clean", Rel: "clean"},
	}
	changes := []git.Change{
		{RepoPath: "D:/repos/clean", RepoRel: "clean", Path: "f", Section: "unstaged", Code: "M"},
	}
	ws.applyScanState("D:/repos", repos, changes)

	if ws.activeRepo != "D:/repos/clean" {
		t.Errorf("activeRepo = %q, want the repo with changes", ws.activeRepo)
	}
	// 只有有改动的仓库进入对应分组；干净仓库不再作为空节点显示
	if len(ws.tree.groups[0].roots) != 0 || len(ws.tree.groups[1].roots) != 1 ||
		len(ws.tree.flatRows) != 2 || len(ws.hist.repoOptions) != 2 {
		t.Errorf("derived data not rebuilt: roots=%d/%d flatRows=%d options=%d",
			len(ws.tree.groups[0].roots), len(ws.tree.groups[1].roots),
			len(ws.tree.flatRows), len(ws.hist.repoOptions))
	}
	// 扫描期间换目录：过期结果作废
	ws.root = "D:/other"
	ws.applyScanState("D:/repos", repos, nil)
	if len(ws.repos) != 2 {
		t.Errorf("stale scan applied after root change")
	}
}

// buildChangeByAbs：同文件多分区只留工作区侧（unstaged > staged），
// 无改动文件不进索引；查表结果与 rebuildDerived 同步重建。
func TestBuildChangeByAbs(t *testing.T) {
	changes := []git.Change{
		{RepoPath: "/r", Path: "a.txt", Section: "staged", Code: "M"},
		{RepoPath: "/r", Path: "a.txt", Section: "unstaged", Code: "M"},
		{RepoPath: "/r", Path: "b.txt", Section: "staged", Code: "A"},
	}
	idx := buildChangeByAbs(changes)
	if got := idx["/r/a.txt"]; got == nil || got.Section != "unstaged" {
		t.Errorf("a.txt = %+v, want worktree (unstaged) side", got)
	}
	if got := idx["/r/b.txt"]; got == nil || got.Section != "staged" {
		t.Errorf("b.txt = %+v, want its only staged entry", got)
	}
	if _, ok := idx["/r/missing.txt"]; ok {
		t.Error("unchanged file should not be indexed")
	}
}
