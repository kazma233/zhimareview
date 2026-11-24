package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"zhimareview/internal/git"
)

// 事件过滤：.git 元数据（目录与 worktree 的 .git 文件）被剔除，"." 全量提示保留。
func TestWatchEventPaths(t *testing.T) {
	events := []mygo.FileEvent{
		{Op: mygo.FileWrite, Path: "demo/src/app.go"},
		{Op: mygo.FileWrite, Path: "demo/.git/index"},
		{Op: mygo.FileWrite, Path: ".git"},
		{Op: mygo.FileWrite, Path: "wtree/.git"},
		{Op: mygo.FileRemove, Path: "loose.txt"},
		{Op: mygo.FileHint, Path: "."},
		{Op: mygo.FileWrite, Path: ""},
	}
	paths := watchEventPaths(events)
	want := []string{"demo/src/app.go", "loose.txt", "."}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

// hitHint：目录级提示命中其下所有路径；"." 全量命中。
func TestHitHint(t *testing.T) {
	root := "D:/repos"
	if !hitHint(root, "demo", root+"/demo/src/app.go") {
		t.Error("dir hint should hit file under it")
	}
	if !hitHint(root, "demo/src", root+"/demo/src/app.go") {
		t.Error("nested dir hint should hit file")
	}
	if hitHint(root, "demo", root+"/other/f.txt") {
		t.Error("hint should not hit sibling path")
	}
	if !hitHint(root, ".", root+"/anything") {
		t.Error("dot hint should hit everything")
	}
	if !hitHint(root, "demo", root+"/demo") {
		t.Error("hint should hit its own dir")
	}
	// 反向：事件细于目标（目标目录/仓库覆盖事件路径）
	if !hitHint(root, "demo/src/app.go", root+"/demo") {
		t.Error("file under target should hit the target")
	}
	if hitHint(root, "other/f.txt", root+"/demo") {
		t.Error("event outside target should not hit it")
	}
}

// mergeChanges：受影响仓库条目被替换，其余保持。
func TestMergeChanges(t *testing.T) {
	ws := newWorkspace(nil)
	ws.changes = []git.Change{
		{RepoPath: "/r/a", RepoRel: "a", Path: "f1", Section: "unstaged", Code: "M"},
		{RepoPath: "/r/b", RepoRel: "b", Path: "f2", Section: "untracked", Code: "?"},
	}
	fresh := []git.Change{
		{RepoPath: "/r/a", RepoRel: "a", Path: "f1", Section: "staged", Code: "M"},
	}
	ws.mergeChanges(fresh, map[string]bool{"/r/a": true})
	if len(ws.changes) != 2 {
		t.Fatalf("changes = %d, want 2", len(ws.changes))
	}
	if ws.changes[0].RepoPath != "/r/b" {
		t.Errorf("changes[0] = %+v, want kept b entry", ws.changes[0])
	}
	if ws.changes[1].Section != "staged" {
		t.Errorf("changes[1] = %+v, want fresh staged entry", ws.changes[1])
	}
}

// structureChanged：仓库集合增删感知，顺序无关。
func TestStructureChanged(t *testing.T) {
	ws := newWorkspace(nil)
	ws.repos = []git.Repo{{Rel: "a"}, {Rel: "b"}}
	if ws.structureChanged([]git.Repo{{Rel: "b"}, {Rel: "a"}}) {
		t.Error("same set should be unchanged")
	}
	if !ws.structureChanged([]git.Repo{{Rel: "b"}}) {
		t.Error("removal should be detected")
	}
	if !ws.structureChanged([]git.Repo{{Rel: "a"}, {Rel: "b"}, {Rel: "c"}}) {
		t.Error("addition should be detected")
	}
}

// 事件命中打开中的 diff：内容重载（headless 下 goroutine 同步落地）。
func TestHandleFsEventsReloadDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\nv2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	change := ws.changes[0]
	ws.openDiff(change)
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.sides.newValue != "base\nv2\n" {
		t.Fatalf("initial diff sides = %+v, want v2 content", ws.diffv.sides)
	}

	// 外部把文件改回基线 → 事件重载 diff
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"base.txt"})
	waitFor(t, func() bool {
		return ws.diffv.sides != nil && ws.diffv.sides.newValue == "base\n"
	}, 5*time.Second, "diff did not reload on fs event")
}

// 事件命中仓库 → 管线重采合并：受影响仓库条目被替换、派生行重建
// （headless 下同步落地；"." 是系统丢失变更后的全量提示）。
func TestHandleFsEventsCollectMerge(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\nv2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	if len(ws.changes) != 1 {
		t.Fatalf("changes = %d, want 1 unstaged entry", len(ws.changes))
	}
	// 工作区改回基线（变干净）→ 全量事件触发重采，合并后条目清空
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"."})
	if len(ws.changes) != 0 {
		t.Fatalf("changes = %d after full-flush event, want merged to 0", len(ws.changes))
	}
	// 派生行同步重建：文件行清空，只剩空仓库的普通文字行（branch、无状态字母、计数 0）
	if len(ws.tree.rows) != 1 || !ws.tree.rows[0].branch ||
		ws.tree.rows[0].letter != "" || ws.tree.rows[0].count != 0 {
		t.Errorf("changeRows = %+v, want only the empty-repo plain row", ws.tree.rows)
	}
}

// watch 合并落地比对：受影响仓库的改动段与当前一致（hint 命中但内容没变）
// 时不重建派生，真变了才合并——实抓：合并走了整体替换重建。
func TestMergeChangesSkipsUnchanged(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	p1 := ws.changeByAbs[root+"/base.txt"]

	// 原样重存（编辑器保存未改内容）：重读段一致 → 不重建。纯内容修改
	// 不算变化——比对对象是改动列表（git 状态），内容级修改产生的列表
	// 逐值相同，正确地被跳过
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"base.txt"})
	if ws.changeByAbs[root+"/base.txt"] != p1 {
		t.Fatal("derived index rebuilt on unchanged merge")
	}

	// 改动列表真变了（未暂存 → 暂存）：合并并重建
	gitRun(t, root, "add", "base.txt")
	ws.handleFsEvents(root, []string{"base.txt"})
	if ws.changeByAbs[root+"/base.txt"] == p1 {
		t.Fatal("derived index not rebuilt on changed merge")
	}
}
