package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// 层级树的行点击契约：改动树默认全展开，点 branch 行收起该层子树
// （已选中的行也不例外），再点重新展开；点叶子行打开 diff。
func TestChangesTreeToggle(t *testing.T) {
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
		t.Fatalf("changes = %d, want 1", len(ws.changes))
	}
	rootLabel := ws.tree.groups[1].roots[0].label

	tt := ui.NewTester(ws.view, 300, 600)
	tt.Frame()
	tt.Frame()

	// 默认全展开：文件行直接可见
	if !tt.HasText("base.txt") {
		t.Fatalf("tree not expanded by default: %q", tt.Texts())
	}

	// 点根仓库行（branch）→ 收起，子文件消失
	if err := tt.Click(rootLabel); err != nil {
		t.Fatalf("click root: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("base.txt") {
		t.Fatalf("first click did not collapse")
	}

	// 再点同一行 → 重新展开（List 的 Changed 只在选区变化时给信号，
	// branch 行点击后选区归 -1，重复点击仍是新选择）
	if err := tt.Click(rootLabel); err != nil {
		t.Fatalf("click root again: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("base.txt") {
		t.Fatalf("second click did not re-expand")
	}

	// 第三次点击 → 再收起（重复点击契约）
	if err := tt.Click(rootLabel); err != nil {
		t.Fatalf("click root third: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("base.txt") {
		t.Fatalf("third click did not collapse")
	}

	// 展开后点叶子行 → 打开 diff
	if err := tt.Click(rootLabel); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if err := tt.Click("base.txt"); err != nil {
		t.Fatalf("click leaf: %v", err)
	}
	waitFor(t, func() bool { return ws.diffv.open != nil }, 5*time.Second, "leaf click did not open diff")
}

// 列表布局的行点击契约：叶子行经条目 id（与树文件行同构）打开对应 diff，
// 不依赖 flat 下标与行集对齐。
func TestFlatListClickOpensDiff(t *testing.T) {
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
	ws.tree.layout = true
	if len(ws.tree.flatRows) == 0 {
		t.Fatal("no flat rows")
	}
	// 第一行是组标题，文件行取首个非标题行
	leaf := ""
	for _, r := range ws.tree.flatRows {
		if !r.header {
			leaf = r.label
			break
		}
	}
	if leaf == "" {
		t.Fatal("no flat file row")
	}

	tt := ui.NewTester(ws.view, 300, 600)
	tt.Frame()
	tt.Frame()
	if err := tt.Click(leaf); err != nil {
		t.Fatalf("click flat row %q: %v", leaf, err)
	}
	waitFor(t, func() bool { return ws.diffv.open != nil }, 5*time.Second, "flat row click did not open diff")
	if got := ws.diffv.open.Path; got != "base.txt" {
		t.Errorf("opened diff = %q, want base.txt", got)
	}
}
