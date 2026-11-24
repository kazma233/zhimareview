package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// menuLabels 右键指定行并返回菜单项标签，随后关闭菜单。
func menuLabels(t *testing.T, tt *ui.Tester, label string) []string {
	t.Helper()
	if err := tt.RightClick(label); err != nil {
		t.Fatalf("right click %q: %v", label, err)
	}
	items := tt.Menu()
	tt.CloseMenu()
	return items
}

// wantMenu 断言菜单项集合与顺序。
func wantMenu(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("menu = %v, want %v", got, want)
	}
}

// diff 列表行右键：文件按分区给暂存/取消暂存，仓库内目录与仓库节点给
// 目录暂存，跨仓库虚拟父层只给复制；复制名称取路径最后一段（平铺布局的
// 显示名是完整相对路径，不能拿 label 当名称）。
func TestChangeListRowMenus(t *testing.T) {
	root := "D:/ws"
	repos := []git.Repo{
		{Path: root + "/group/a", Name: "a", Rel: "group/a"},
		{Path: root + "/group/b", Name: "b", Rel: "group/b"},
	}
	changes := []git.Change{
		{RepoPath: root + "/group/a", RepoRel: "group/a", Path: "src/app.go", Section: "unstaged", Code: "M"},
		{RepoPath: root + "/group/a", RepoRel: "group/a", Path: "staged.txt", Section: "staged", Code: "A"},
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.repos = repos
	ws.changes = changes
	ws.activeRepo = repos[0].Path
	ws.rebuildDerived()

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()

	wantMenu(t, menuLabels(t, tt, "app.go"), "暂存", "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "staged.txt"), "取消暂存", "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "src"), "暂存目录", "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "a"), "暂存仓库", "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "group"), "复制路径", "复制名称")

	// 复制路径是绝对路径，复制名称是最后一段
	if err := tt.RightClick("app.go"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制路径"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got, want := tt.Clipboard(), root+"/group/a/src/app.go"; got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}

	if err := tt.RightClick("app.go"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制名称"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got, want := tt.Clipboard(), "app.go"; got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}

	// 平铺布局：行显示完整相对路径，复制名称仍是文件名
	ws.tree.layout = true
	tt.Frame()
	if err := tt.RightClick("group/a/src/app.go"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制名称"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got, want := tt.Clipboard(), "app.go"; got != want {
		t.Errorf("flat clipboard = %q, want %q", got, want)
	}
}

// 目录列表行右键：未修改文件只复制；已修改文件加暂存；仓库内目录/仓库根
// 可暂存目录；包含多个仓库的父目录（跨仓库）只复制。
func TestDirListRowMenus(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	write(t, root+"/group/a/src/app.go", "app\n")
	write(t, root+"/group/a/clean.txt", "clean\n")
	write(t, root+"/group/b/main.go", "main\n")
	write(t, root+"/loose.txt", "loose\n")

	repos := []git.Repo{
		{Path: root + "/group/a", Name: "a", Rel: "group/a"},
		{Path: root + "/group/b", Name: "b", Rel: "group/b"},
	}
	changes := []git.Change{
		{RepoPath: root + "/group/a", RepoRel: "group/a", Path: "src/app.go", Section: "unstaged", Code: "M"},
		{RepoPath: root + "/group/b", RepoRel: "group/b", Path: "main.go", Section: "unstaged", Code: "M"},
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.repos = repos
	ws.changes = changes
	ws.activeRepo = repos[0].Path
	ws.sidebarTab = "dir"
	ws.rebuildDerived()
	ws.dirs.cache = map[string][]fsEntry{
		root:                  readDirEntries(root),
		root + "/group":       readDirEntries(root + "/group"),
		root + "/group/a":     readDirEntries(root + "/group/a"),
		root + "/group/a/src": readDirEntries(root + "/group/a/src"),
		root + "/group/b":     readDirEntries(root + "/group/b"),
	}
	ws.dirs.expanded = map[string]bool{
		root: true, root + "/group": true, root + "/group/a": true,
		root + "/group/a/src": true, root + "/group/b": true,
	}
	ws.dirs.rebuildRows(root, changes)

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()

	wantMenu(t, menuLabels(t, tt, "app.go"), "暂存", "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "clean.txt"), "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "loose.txt"), "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "src"), "暂存目录", "复制路径", "复制名称")
	wantMenu(t, menuLabels(t, tt, "a"), "暂存仓库", "复制路径", "复制名称")
	// group 目录包含两个仓库：跨仓库，不提供目录暂存
	wantMenu(t, menuLabels(t, tt, "group"), "复制路径", "复制名称")
}

// 右键暂存目录：只暂存该目录内、该仓库的改动；打开中的 diff 若不在范围
// 内不得被翻转。
func TestStageDirFromRowMenu(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/src/tracked.txt", "tracked\n")
	gitRun(t, root, "add", "src/tracked.txt")
	gitRun(t, root, "commit", "-m", "add tracked")
	write(t, root+"/src/tracked.txt", "tracked\nchanged\n")
	write(t, root+"/src/new.txt", "new\n")
	write(t, root+"/base.txt", "base\nchanged\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))

	// 打开仓库根 base.txt 的 diff：它不在 src 范围内
	base := *ws.changeForAbs(root + "/base.txt")
	ws.openDiff(base)

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.RightClick("src"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("暂存目录"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	waitFor(t, func() bool {
		st, err := git.ReadStatus(root)
		if err != nil || len(st.Staged) != 2 || len(st.Unstaged) != 1 {
			return false
		}
		return true
	}, 5*time.Second, "stage dir did not apply")

	st, err := git.ReadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	staged := map[string]bool{}
	for _, f := range st.Staged {
		staged[f.Path] = true
	}
	if !staged["src/tracked.txt"] || !staged["src/new.txt"] {
		t.Errorf("staged = %v, want src/tracked.txt and src/new.txt", st.Staged)
	}
	if ws.diffv.open == nil || ws.diffv.open.Section != "unstaged" {
		t.Errorf("open diff = %+v, want base.txt still unstaged (outside staged dir)", ws.diffv.open)
	}
}

// 右键暂存目录：打开中的 diff 在范围内且原本未暂存时，会话内翻到 staged。
func TestStageDirFlipsOpenDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/src/f.txt", "f\n")
	gitRun(t, root, "add", "src/f.txt")
	gitRun(t, root, "commit", "-m", "add f")
	write(t, root+"/src/f.txt", "f\nchanged\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	ch := *ws.changeForAbs(root + "/src/f.txt")
	ws.openDiff(ch)

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.RightClick("src"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("暂存目录"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ws.diffv.open == nil || ws.diffv.open.Section != "staged" {
		t.Fatalf("open diff = %+v, want flipped to staged", ws.diffv.open)
	}
}

// 提交列表行右键：复制 hash 与 revert；合并提交 revert 禁用。
func TestCommitRowMenu(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.activeRepo = "D:/repos/a"
	ws.hist.all = []git.CommitRow{
		{Hash: "abcdef123456", ShortHash: "abcdef1", Author: "a", Date: "d", Subject: "s"},
		{Hash: "fedcba654321", ShortHash: "fedcba6", Author: "a", Date: "d", Subject: "merge x", Merge: true},
	}
	ws.hist.commits = ws.hist.all
	ws.hist.done = true

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()

	wantMenu(t, menuLabels(t, tt, "abcdef1"), "复制 hash", "revert")
	wantMenu(t, menuLabels(t, tt, "fedcba6"), "复制 hash", "revert")

	if err := tt.RightClick("fedcba6"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("revert"); err == nil {
		t.Fatal("merge commit revert should be disabled")
	}
	tt.CloseMenu()
}

// 右键 revert：从列表行直接撤销该提交，历史出现 Revert 提交。
func TestRevertFromRowMenu(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/f.txt", "f\n")
	gitRun(t, root, "add", "f.txt")
	gitRun(t, root, "commit", "-m", "add f")

	rows, err := git.LogCommits(root, 0, 10)
	if err != nil || len(rows) == 0 {
		t.Fatalf("log commits: %v (%d rows)", err, len(rows))
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.mainTab = 1
	ws.activeRepo = root
	ws.repos = []git.Repo{{Path: root, Name: filepath.Base(root), Rel: ""}}
	ws.hist.all = rows
	ws.hist.commits = rows
	ws.hist.done = true
	ws.hist.allLoaded = true

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.RightClick(rows[0].ShortHash); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("revert"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	waitFor(t, func() bool {
		logs, err := git.LogCommits(root, 0, 10)
		if err != nil {
			return false
		}
		for _, r := range logs {
			if strings.HasPrefix(r.Subject, "Revert ") {
				return true
			}
		}
		return false
	}, 10*time.Second, "revert commit not created")
}
