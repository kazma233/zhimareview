package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"zhimareview/internal/git"
)

// carriedChanges：读取失败的仓库沿用旧条目，成功仓库用新结果，按 found
// 顺序输出；已从扫描结果消失的仓库不再携带。
func TestCarriedChangesKeepsFailedRepos(t *testing.T) {
	a := git.Repo{Path: "/r/a", Name: "a", Rel: "a"}
	b := git.Repo{Path: "/r/b", Name: "b", Rel: "b"}
	previous := []git.Change{
		{RepoPath: a.Path, Path: "old-a.txt", Section: "unstaged", Code: "M"},
		{RepoPath: "/r/gone", Path: "gone.txt", Section: "unstaged", Code: "M"},
		{RepoPath: b.Path, Path: "old-b.txt", Section: "unstaged", Code: "M"},
	}
	fresh := []git.Change{
		{RepoPath: b.Path, Path: "new-b.txt", Section: "staged", Code: "A"},
	}
	failed := map[string]error{a.Path: errors.New("index locked"), "/r/gone": errors.New("gone")}
	got := carriedChanges(previous, []git.Repo{b, a}, fresh, failed)
	want := []git.Change{
		{RepoPath: b.Path, Path: "new-b.txt", Section: "staged", Code: "A"},
		{RepoPath: a.Path, Path: "old-a.txt", Section: "unstaged", Code: "M"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("carried = %+v, want %+v", got, want)
	}
	// 没有失败仓库：原样使用采集结果
	if got := carriedChanges(previous, []git.Repo{a, b}, fresh, nil); !slices.Equal(got, fresh) {
		t.Errorf("no failure should keep fresh changes verbatim: %+v", got)
	}
}

// 采集失败不等于改动消失：git status 读取失败的仓库沿用旧条目，重扫与
// watch 两条路径都不把打开中的 diff 误关掉。
func TestStatusFailureKeepsOpenDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/base.txt", "base\nchanged\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	if len(ws.changes) != 1 {
		t.Fatalf("changes = %+v, want 1", ws.changes)
	}
	ws.openDiff(ws.changes[0])
	waitDiffLoaded(t, ws)

	// 损坏 index：git status 读取失败（先确认夹具有效，避免空跑）
	if err := os.WriteFile(filepath.Join(root, ".git", "index"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git.ReadStatus(root); err == nil {
		t.Fatal("fixture ineffective: corrupt index still readable")
	}

	// 重扫：失败仓库沿用旧条目
	ws.rescan(root)
	if len(ws.changes) != 1 || ws.diffv.open == nil {
		t.Fatalf("rescan treated failure as no changes: changes=%+v open=%v", ws.changes, ws.diffv.open != nil)
	}
	// watch 重采合并：同一保护
	ws.handleFsEvents(root, []string{"."})
	if len(ws.changes) != 1 || ws.diffv.open == nil {
		t.Fatalf("watch merge treated failure as no changes: changes=%+v open=%v", ws.changes, ws.diffv.open != nil)
	}
}

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
