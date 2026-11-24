package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// 分组模型：已暂存在上、未暂存在下，untracked 归未暂存；组标题带计数，
// 干净仓库不再作为空节点出现在任何一组。
func TestChangesGroupedBySection(t *testing.T) {
	root := "D:/repos"
	repos := []git.Repo{
		{Path: root + "/demo", Name: "demo", Rel: "demo"},
		{Path: root + "/clean", Name: "clean", Rel: "clean"},
	}
	staged := git.Change{RepoPath: root + "/demo", RepoRel: "demo", Path: "staged.txt", Section: "staged", Code: "M"}
	work := git.Change{RepoPath: root + "/demo", RepoRel: "demo", Path: "work.txt", Section: "unstaged", Code: "M"}
	untracked := git.Change{RepoPath: root + "/demo", RepoRel: "demo", Path: "new.txt", Section: "untracked", Code: "?"}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.repos = repos
	ws.changes = []git.Change{untracked, staged, work} // 输入顺序无关，按分区归组
	ws.activeRepo = root + "/demo"
	ws.rebuildDerived()

	var ids []string
	for _, r := range ws.tree.rows {
		ids = append(ids, r.id)
	}
	want := []string{
		groupID("staged"), repoID("staged", root+"/demo"), fileID(staged),
		groupID("unstaged"), repoID("unstaged", root+"/demo"), fileID(untracked), fileID(work),
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("rows = %v\nwant %v", ids, want)
	}
	if !ws.tree.rows[0].header || ws.tree.rows[0].count != 1 {
		t.Errorf("staged header = %+v, want header with count 1", ws.tree.rows[0])
	}
	if !ws.tree.rows[3].header || ws.tree.rows[3].count != 2 {
		t.Errorf("unstaged header = %+v, want header with count 2", ws.tree.rows[3])
	}
	// 干净仓库不进入任何一组
	for _, r := range ws.tree.rows {
		if r.id == repoID("staged", root+"/clean") || r.id == repoID("unstaged", root+"/clean") {
			t.Errorf("clean repo row %+v should not appear", r)
		}
	}
}

// 同一文件同时有已暂存与未暂存改动时，两组各出现一行（fileID 带分区）。
func TestChangesSameFileInBothGroups(t *testing.T) {
	root := "D:/repos"
	repo := git.Repo{Path: root + "/demo", Name: "demo", Rel: "demo"}
	changes := []git.Change{
		{RepoPath: repo.Path, RepoRel: "demo", Path: "dup.txt", Section: "staged", Code: "M"},
		{RepoPath: repo.Path, RepoRel: "demo", Path: "dup.txt", Section: "unstaged", Code: "M"},
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.repos = []git.Repo{repo}
	ws.changes = changes
	ws.activeRepo = repo.Path
	ws.rebuildDerived()

	seen := map[string]bool{}
	for _, r := range ws.tree.rows {
		seen[r.id] = true
	}
	for _, c := range changes {
		if !seen[fileID(c)] {
			t.Errorf("row %q missing from its section group", fileID(c))
		}
	}
}

// 组折叠：点标题只收起该组行，标题保留；组内目录 id 带分区，两组独立开合。
func TestChangesGroupCollapseAndDirToggle(t *testing.T) {
	root := "D:/repos"
	repo := git.Repo{Path: root + "/demo", Name: "demo", Rel: "demo"}
	staged := git.Change{RepoPath: repo.Path, RepoRel: "demo", Path: "src/a.go", Section: "staged", Code: "M"}
	work := git.Change{RepoPath: repo.Path, RepoRel: "demo", Path: "src/b.go", Section: "unstaged", Code: "M"}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.repos = []git.Repo{repo}
	ws.changes = []git.Change{staged, work}
	ws.activeRepo = repo.Path
	ws.rebuildDerived()

	// 两组各有自己的目录行（id 带分区），折叠已暂存组的 src 不影响未暂存组
	ws.toggleChange(dirID("staged", "demo/src"))
	open := map[string]bool{}
	for _, r := range ws.tree.rows {
		open[r.id] = r.open
	}
	if open[dirID("staged", "demo/src")] {
		t.Error("staged src should be collapsed")
	}
	if !open[dirID("unstaged", "demo/src")] {
		t.Error("unstaged src must stay expanded")
	}
	// 恢复目录展开，后续只验证组折叠
	ws.toggleChange(dirID("staged", "demo/src"))

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	tt.Frame()
	// 点组标题：组内行隐藏、标题保留，不打开 diff
	if err := tt.Click("已暂存"); err != nil {
		t.Fatalf("click staged header: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("a.go") {
		t.Error("staged file row should hide under collapsed group")
	}
	if !tt.HasText("已暂存") || !tt.HasText("未暂存") {
		t.Errorf("group headers missing: %q", tt.Texts())
	}
	if !tt.HasText("b.go") {
		t.Error("unstaged group must stay visible")
	}
	if ws.diffv.open != nil {
		t.Errorf("group header click opened a diff: %+v", ws.diffv.open)
	}
	// 再点恢复
	if err := tt.Click("已暂存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("a.go") {
		t.Error("second click did not re-expand the group")
	}
}

// 平铺布局同样按分区分组：组标题 + 仓库相对路径行；组折叠对平铺行生效。
func TestChangesFlatLayoutGrouped(t *testing.T) {
	root := "D:/repos"
	repo := git.Repo{Path: root + "/demo", Name: "demo", Rel: "demo"}
	changes := []git.Change{
		{RepoPath: repo.Path, RepoRel: "demo", Path: "staged.txt", Section: "staged", Code: "M"},
		{RepoPath: repo.Path, RepoRel: "demo", Path: "work.txt", Section: "unstaged", Code: "M"},
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.repos = []git.Repo{repo}
	ws.changes = changes
	ws.activeRepo = repo.Path
	ws.rebuildDerived()
	ws.tree.layout = true

	var labels []string
	for _, r := range ws.tree.flatRows {
		labels = append(labels, r.label)
	}
	want := []string{"已暂存", "demo/staged.txt", "未暂存", "demo/work.txt"}
	if !slices.Equal(labels, want) {
		t.Fatalf("flat rows = %v, want %v", labels, want)
	}

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("未暂存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("demo/work.txt") {
		t.Error("flat row should hide under collapsed group")
	}
	if !tt.HasText("demo/staged.txt") {
		t.Error("staged flat row must stay visible")
	}
}

// 组标题行没有右键菜单；文件行点击仍打开对应分区的 diff。
func TestGroupHeaderMenuAndFileClick(t *testing.T) {
	root := "D:/repos"
	repo := git.Repo{Path: root + "/demo", Name: "demo", Rel: "demo"}
	staged := git.Change{RepoPath: repo.Path, RepoRel: "demo", Path: "staged.txt", Section: "staged", Code: "M"}
	work := git.Change{RepoPath: repo.Path, RepoRel: "demo", Path: "work.txt", Section: "unstaged", Code: "M"}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.repos = []git.Repo{repo}
	ws.changes = []git.Change{staged, work}
	ws.activeRepo = repo.Path
	ws.rebuildDerived()

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	if err := tt.RightClick("已暂存"); err != nil {
		t.Fatalf("right click group header: %v", err)
	}
	if items := tt.Menu(); items != nil {
		t.Errorf("group header menu = %v, want none", items)
	}
	tt.CloseMenu()

	if err := tt.Click("staged.txt"); err != nil {
		t.Fatal(err)
	}
	if ws.diffv.open == nil || ws.diffv.open.Section != "staged" {
		t.Fatalf("open diff = %+v, want staged.txt in staged section", ws.diffv.open)
	}
}
