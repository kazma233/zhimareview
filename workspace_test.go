package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// 工作区视图：顶栏统计、层级树收起只显示仓库行、列表布局显示完整路径。
func TestWorkView(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	repos := []git.Repo{{Path: "D:/repos/demo", Name: "demo", Rel: "demo"}}
	changes := []git.Change{
		{RepoPath: "D:/repos/demo", RepoRel: "demo", Path: "src/app.go", Section: "unstaged", Code: "M"},
		{RepoPath: "D:/repos/demo", RepoRel: "demo", Path: "b.txt", Section: "untracked", Code: "?"},
	}
	ws.applyScanState("D:/repos", repos, changes)

	tt := ui.NewTester(ws.view, 1180, 800)
	if !tt.HasText("1 仓库 · 2 文件") {
		t.Errorf("texts = %q, want stats", tt.Texts())
	}
	// 层级树默认全展开：仓库行与文件行都可见
	if !tt.HasText("demo") {
		t.Errorf("texts = %q, want repo row", tt.Texts())
	}
	if !tt.HasText("app.go") {
		t.Errorf("texts = %q, want files visible under default-expanded tree", tt.Texts())
	}

	// 列表布局：仓库/文件完整路径可见
	ws.tree.layout = true
	tt = ui.NewTester(ws.view, 1180, 800)
	if !tt.HasText("demo/src/app.go") {
		t.Errorf("texts = %q, want flat rows", tt.Texts())
	}
}
