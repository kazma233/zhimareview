package main

// 侧栏三视图的滚动回归：改动树曾因 TreeBase 未真正进入 Scroll（挂载顺序）
// 而整树不可滚；统一为拍平行 + ui.List 后三处都必须可滚。
// 断言方式：List 只构建可见行——滚走的首行消失、原本在视口外的行出现，
// 滚回后首行复现。任何一处不可滚，行集合都不会变化。

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// scrollWorkspace 造一个有 n 个改动文件的根仓库工作区（改动树/列表布局用）。
func scrollWorkspace(t *testing.T, n int) *workspace {
	t.Helper()
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	repos := []git.Repo{{Path: "D:/repos/demo", Name: "demo", Rel: ""}}
	changes := make([]git.Change, 0, n)
	for i := range n {
		changes = append(changes, git.Change{
			RepoPath: "D:/repos/demo", Path: fmt.Sprintf("f%03d.txt", i),
			Section: "unstaged", Code: "M",
		})
	}
	ws.applyScanState("D:/repos", repos, changes)
	return ws
}

// wheelAtSidebar 在侧栏内容区滚一格并等滚轮滑行动画落定。
func wheelAtSidebar(t *testing.T, tt *ui.Tester, dy float32) {
	t.Helper()
	tt.Scroll(150, 400, 0, dy)
	tt.Advance(300 * time.Millisecond)
}

// assertScrollCycle 断言 down 行可滚出视口、far 行滚入、反向滚动后 down 复现。
func assertScrollCycle(t *testing.T, tt *ui.Tester, down, far string) {
	t.Helper()
	if _, ok := tt.Find(down); !ok {
		t.Fatalf("before scroll: %q not visible", down)
	}
	wheelAtSidebar(t, tt, 600)
	if _, ok := tt.Find(down); ok {
		t.Fatalf("scroll down: %q still visible, content did not move", down)
	}
	if _, ok := tt.Find(far); !ok {
		t.Fatalf("scroll down: %q should be visible after scrolling", far)
	}
	wheelAtSidebar(t, tt, -600)
	if _, ok := tt.Find(down); !ok {
		t.Fatalf("scroll back: %q should be visible again", down)
	}
}

// 改动层级树：默认全展开后 121+ 行，远超视口。
func TestSidebarScrollsChangesTree(t *testing.T) {
	ws := scrollWorkspace(t, 120)

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	assertScrollCycle(t, tt, "f000.txt", "f040.txt")
}

// 改动列表布局：同一 List 模式的另一套拍平。
func TestSidebarScrollsFlatList(t *testing.T) {
	ws := scrollWorkspace(t, 120)
	ws.tree.layout = true

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	assertScrollCycle(t, tt, "demo/f000.txt", "demo/f040.txt")
}

// 目录列表：真实文件树，对照组。
func TestSidebarScrollsDirTree(t *testing.T) {
	dir := t.TempDir()
	for i := range 120 {
		if err := os.WriteFile(fmt.Sprintf("%s/f%03d.txt", dir, i), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = dir
	// 目录模式状态（dirs.cache/dirs.loading）由 applyScanState 初始化，
	// 这里空扫描只是走初始化分支，仓库发现为空不影响目录树
	ws.applyScanState(dir, nil, nil)
	ws.sidebarTab = "dir"
	ws.loadDir(dir)

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	assertScrollCycle(t, tt, "f000.txt", "f040.txt")
}

// 中等行数（内容略超视口）的滚动：曾经视口被 Fill 失真到与内容等高，
// 恰好无滚动余量。
func TestSidebarScrollsModerateRows(t *testing.T) {
	ws := scrollWorkspace(t, 40)

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	assertScrollCycle(t, tt, "f000.txt", "f030.txt")
}
