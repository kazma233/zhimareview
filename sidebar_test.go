package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// 仓库折叠进目录层级、干净仓库是空节点、颜色继承最重子孙、根仓库单行承载。
func TestBuildChangesTree(t *testing.T) {
	repos := []git.Repo{
		{Path: "/r/demo", Name: "demo", Rel: "demo"},
		{Path: "/r/group/a", Name: "a", Rel: "group/a"},
		{Path: "/r/group/b", Name: "b", Rel: "group/b"},
		{Path: "/r/root-repo", Name: "root-repo", Rel: ""},
	}
	changes := []git.Change{
		{RepoPath: "/r/demo", RepoRel: "demo", Path: "src/app.go", Section: "unstaged", Code: "M"},
		{RepoPath: "/r/demo", RepoRel: "demo", Path: "main.go", Section: "untracked", Code: "?"},
		{RepoPath: "/r/group/a", RepoRel: "group/a", Path: "f.txt", Section: "staged", Code: "D"},
	}

	roots, index := buildChangesTree("/r", repos, changes)

	// 根仓库存在：整棵树挂根仓库单行下
	if len(roots) != 1 {
		t.Fatalf("roots = %d items, want 1 (root repo carries all)", len(roots))
	}
	rootRepo := roots[0]
	if !rootRepo.isRepo || rootRepo.label != "root-repo" {
		t.Fatalf("root = %+v, want root-repo repo node", rootRepo)
	}
	byLabel := map[string]*treeItem{}
	for _, r := range rootRepo.kids {
		byLabel[r.label] = r
	}
	// 根仓库行承载整棵子树的统计与状态继承（demo 2 + group/a 1）
	if rootRepo.count != 3 || rootRepo.rank != 5 {
		t.Errorf("root repo item = %+v, want count 3 rank 5", rootRepo)
	}
	demo := byLabel["demo"]
	if demo == nil || demo.count != 2 {
		t.Fatalf("demo item = %+v, want 2 changes", demo)
	}
	// demo 继承子孙最重状态：M(4) 与 U(2) → 4
	if demo.rank != 4 {
		t.Errorf("demo rank = %d, want 4", demo.rank)
	}
	// group 目录继承 a 的 D(5)
	group := byLabel["group"]
	if group == nil || group.rank != 5 || group.count != 1 {
		t.Errorf("group item = %+v, want rank 5 count 1", group)
	}
	a, ok := index[repoID("/r/group/a")]
	if !ok || a.rank != 5 {
		t.Errorf("repo a = %+v, want rank 5", a)
	}
	// 文件行带状态字母
	app := index["cf:/r/demo|src/app.go|unstaged"]
	if app == nil || app.letter != "M" || app.rank != 4 {
		t.Errorf("app.go item = %+v, want M rank 4", app)
	}
	// 目录节点的子节点数量
	if len(demo.kids) != 2 {
		t.Errorf("demo kids = %d, want 2", len(demo.kids))
	}
}

// 改名条目按新路径分组：git mv aidocs docs 后文件挂在 docs 下（带 R 字母），
// 旧目录 aidocs 不出现——实抓：Path 混入箭头串时树里只有旧目录、新文件丢失。
func TestBuildChangesTreeRenameGroupsByNewPath(t *testing.T) {
	repos := []git.Repo{{Path: "/r/reins", Name: "reins", Rel: ""}}
	changes := []git.Change{
		{RepoPath: "/r/reins", Path: "docs/AGENTS.md", OrigPath: "aidocs/AGENTS.md", Section: "staged", Code: "R"},
		{RepoPath: "/r/reins", Path: "docs/Ref.md", OrigPath: "aidocs/Ref.md", Section: "staged", Code: "R"},
	}
	roots, index := buildChangesTree("/r", repos, changes)

	var labels []string
	var walk func(items []*treeItem)
	walk = func(items []*treeItem) {
		for _, it := range items {
			labels = append(labels, it.label)
			walk(it.kids)
		}
	}
	walk(roots)
	for _, l := range labels {
		if l == "aidocs" || strings.Contains(l, "->") {
			t.Fatalf("old dir or arrow leaked into tree: %q (all: %v)", l, labels)
		}
	}
	docs := index[dirID("docs")]
	if docs == nil || docs.count != 2 {
		t.Fatalf("docs item = %+v, want 2 renamed files", docs)
	}
	agent := index["cf:/r/reins|docs/AGENTS.md|staged"]
	if agent == nil || agent.letter != "R" {
		t.Errorf("docs/AGENTS.md item = %+v, want letter R", agent)
	}
}

// 文件按名字排序、目录按名字排序，输出稳定。
func TestBuildChangesTreeStableOrder(t *testing.T) {
	repos := []git.Repo{{Path: "/r/x", Name: "x", Rel: "x"}}
	changes := []git.Change{
		{RepoPath: "/r/x", RepoRel: "x", Path: "z.txt", Section: "unstaged", Code: "M"},
		{RepoPath: "/r/x", RepoRel: "x", Path: "a/b.txt", Section: "untracked", Code: "?"},
		{RepoPath: "/r/x", RepoRel: "x", Path: "a.txt", Section: "unstaged", Code: "M"},
	}
	roots, _ := buildChangesTree("/r", repos, changes)
	x := roots[0]
	if len(x.kids) != 3 {
		t.Fatalf("x kids = %d, want dir a + files a.txt/z.txt", len(x.kids))
	}
	// 顶层排序：目录 a 在前，文件 a.txt、z.txt 在后
	if x.kids[0].label != "a" || x.kids[1].label != "a.txt" || x.kids[2].label != "z.txt" {
		t.Errorf("order = %s %s %s, want a a.txt z.txt",
			x.kids[0].label, x.kids[1].label, x.kids[2].label)
	}
}

func TestSortChangesOrder(t *testing.T) {
	changes := []git.Change{
		{RepoRel: "b", Path: "f", Section: "staged"},
		{RepoRel: "a", Path: "z", Section: "staged"},
		{RepoRel: "a", Path: "a", Section: "staged"},
		{RepoRel: "a", Path: "a", Section: "unstaged"},
	}
	got := sortChanges(changes)
	// 排序契约是三段字典序：staged < unstaged
	want := []string{"a:a:staged", "a:a:unstaged", "a:z:staged", "b:f:staged"}
	for i, w := range want {
		g := got[i]
		if g.RepoRel+":"+g.Path+":"+g.Section != w {
			t.Errorf("sorted[%d] = %s, want %s", i, g.RepoRel+":"+g.Path+":"+g.Section, w)
		}
	}
}

func TestStatusRankAndColor(t *testing.T) {
	if l, r := statusRank("?", "untracked"); l != "U" || r != 2 {
		t.Errorf("untracked = %s %d, want U 2", l, r)
	}
	if _, r := statusRank("D", "unstaged"); r != 5 {
		t.Errorf("D rank = %d, want 5", r)
	}
	muted := ui.Hex("#999999")
	tk := tokens{Muted: muted, StatusMod: ui.Hex("#f59e0b")}
	if tk.rankColor(0) != muted || tk.rankColor(1) != muted {
		t.Error("rank 0/1 should be muted")
	}
	if tk.rankColor(4) == muted || tk.rankColor(4) != tk.StatusMod {
		t.Error("rank 4 (M) should be StatusMod")
	}
}

// 单链目录合并：无文件的单链目录压成一行 a/b/c（id 取最深层）；
// 目录带直接文件、单孩子为文件、孩子是仓库节点时不合并。
func TestBuildChangesTreeCompressesDirChains(t *testing.T) {
	repos := []git.Repo{
		{Path: "/r/wrap", Name: "wrap", Rel: ""},
		{Path: "/r/wrap/nest", Name: "nest", Rel: "nest"},
	}
	changes := []git.Change{
		// 单链：a→b→c 每层只有一个目录孩子，文件在最深——压成 a/b/c 一行
		{RepoPath: "/r/wrap", Path: "a/b/c/deep.java", Section: "unstaged", Code: "M"},
		// mix 直接挂文件：mix 不合并；其下单链 sub（孩子是文件）也不合并
		{RepoPath: "/r/wrap", Path: "mix/file.java", Section: "unstaged", Code: "M"},
		{RepoPath: "/r/wrap", Path: "mix/sub/inner.java", Section: "unstaged", Code: "M"},
		// 嵌套仓库节点：不合并，保持仓库行
		{RepoPath: "/r/wrap/nest", RepoRel: "nest", Path: "f.java", Section: "unstaged", Code: "M"},
	}
	roots, index := buildChangesTree("/r", repos, changes)

	byLabel := map[string]*treeItem{}
	for _, r := range roots[0].kids {
		byLabel[r.label] = r
	}
	chain, ok := byLabel["a/b/c"]
	if !ok {
		t.Fatalf("compressed chain row missing: labels %v", func() []string {
			out := []string{}
			for _, r := range roots[0].kids {
				out = append(out, r.label)
			}
			return out
		}())
	}
	if chain.id != "cd:a/b/c" || len(chain.kids) != 1 || chain.kids[0].change == nil {
		t.Errorf("chain row = id %q kids %d, want deepest id with the file inside", chain.id, len(chain.kids))
	}
	if mix := byLabel["mix"]; mix == nil || mix.id != "cd:mix" || len(mix.kids) != 2 {
		t.Errorf("mix row should keep direct file and sub as separate kids")
	}
	if sub := byLabel["sub"]; sub == nil || sub.label != "sub" {
		if byLabel["mix/sub"] != nil {
			t.Error("sub must not merge into its file child")
		}
	}
	if _, ok := byLabel["nest"]; !ok {
		t.Error("nested repo node must stay a standalone row")
	}
	if _, ok := index["cd:a/b/c"]; !ok {
		t.Error("compressed chain id missing from index")
	}
}

// 「diff列表」段内嵌的布局拨杆（layoutToggle）：点击翻转层级树/平铺列表，
// 不切换侧栏段（独立可点元素吃掉点击）；段切换仍由段自身的点击承担，
// 布局状态跨段切换保持。
func TestSidebarLayoutToggle(t *testing.T) {
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

	tt := ui.NewTester(ws.view, 300, 600)
	tt.Frame()
	tt.Frame()

	if ws.tree.layout {
		t.Fatalf("layout = %v, want tree by default", ws.tree.layout)
	}
	if ws.sidebarTab != "changes" {
		t.Fatalf("sidebarTab = %q, want changes", ws.sidebarTab)
	}

	// 点拨杆 → 平铺列表；段仍是 diff列表
	if err := tt.Click("切换列表布局"); err != nil {
		t.Fatalf("click layout toggle: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !ws.tree.layout {
		t.Fatal("toggle did not switch to flat list")
	}
	if ws.sidebarTab != "changes" {
		t.Fatalf("toggle leaked into segment: sidebarTab = %q", ws.sidebarTab)
	}

	// 再点拨杆 → 回层级树
	if err := tt.Click("切换列表布局"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if ws.tree.layout {
		t.Fatal("toggle did not switch back to tree")
	}

	// 点拨杆到列表布局后切「目录列表」段，再切回：布局状态保持
	if err := tt.Click("切换列表布局"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("目录列表"); err != nil {
		t.Fatalf("click dir segment: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if ws.sidebarTab != "dir" {
		t.Fatalf("sidebarTab = %q, want dir", ws.sidebarTab)
	}
	// 目录列表段激活时拨杆禁用：点击不动布局
	if err := tt.Click("切换列表布局"); err != nil {
		t.Fatalf("click disabled toggle: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !ws.tree.layout {
		t.Fatal("disabled toggle changed layout")
	}
	if err := tt.Click("diff列表"); err != nil {
		t.Fatalf("click changes segment: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if ws.sidebarTab != "changes" {
		t.Fatalf("sidebarTab = %q, want changes", ws.sidebarTab)
	}
	if !ws.tree.layout {
		t.Fatal("layout state lost after segment round-trip")
	}
}
