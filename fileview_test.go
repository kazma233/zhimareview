package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// 复制路径：diff 头部与文件查看头部都复制绝对路径。
func TestCopyFilePath(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\nchanged\n"), 0o644); err != nil {
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

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.Click("复制路径"); err != nil {
		t.Fatal(err)
	}
	if got, want := tt.Clipboard(), changeAbs(change); got != want {
		t.Errorf("diff clipboard = %q, want %q", got, want)
	}

	// 文件查看入口同样可复制
	ws.openFile(root + "/base.txt")
	waitFor(t, func() bool { return ws.dirs.file != nil && !ws.dirs.file.loading && ws.dirs.file.kind == "text" },
		5*time.Second, "file not loaded")
	tt.Frame()
	if err := tt.Click("复制路径"); err != nil {
		t.Fatal(err)
	}
	if got, want := tt.Clipboard(), root+"/base.txt"; got != want {
		t.Errorf("file clipboard = %q, want %q", got, want)
	}
}

// 目录列表打开文件的分流契约：「查看 diff」勾选且带改动的文件直接进 diff
// 且默认全展开（ws.file 让位）；不勾选（默认）时带改动的文件也只读查看
// 原内容；无改动的文件任何状态下都走只读查看（editor 状态加载完成）。
func TestOpenFileRoutesToDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	// clean.txt 提交后保持无改动；changed.txt 提交后再改出未暂存改动
	if err := os.WriteFile(filepath.Join(root, "clean.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\nv3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))

	// 默认不勾选「查看 diff」：带改动的文件也只读查看原内容
	if ws.dirs.showDiff {
		t.Fatal("dirShowDiff should default to false")
	}
	ws.openFile(root + "/changed.txt")
	if ws.diffv.open != nil || ws.dirs.file == nil {
		t.Errorf("changed file should open read-only when 查看diff is off: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")

	// 无改动 → 只读查看（勾选与否都一样）
	ws.openFile(root + "/clean.txt")
	if ws.diffv.open != nil || ws.dirs.file == nil {
		t.Errorf("clean file should open as read-only view: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")

	// 勾选「查看 diff」后：带改动 → diff 全展开，文件查看让位
	ws.dirs.showDiff = true
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.open == nil || ws.dirs.file != nil {
		t.Errorf("changed file should open as diff: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
	if !ws.diffv.ed.ExpandUnchanged {
		t.Error("dir-tab diff should default to fully expanded")
	}
}

// 「查看 diff」checkbox 跟随主区内容出现：带改动的文件（只读查看）与目录
// 入口的 diff 头部有，无改动文件与 diff 列表入口的 diff 没有。
func TestDirShowDiffCheckbox(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "clean.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\nv3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	tt := ui.NewTester(ws.view, 1180, 800)

	// 带改动文件只读查看：头部出现 checkbox
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")
	tt.Frame()
	if !tt.HasText("查看 diff") {
		t.Fatal("changed file view should show 查看diff checkbox")
	}

	// 无改动文件：没有 checkbox
	ws.openFile(root + "/clean.txt")
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")
	tt.Frame()
	if tt.HasText("查看 diff") {
		t.Error("clean file view should not show 查看diff checkbox")
	}

	// 目录入口的 diff：头部出现 checkbox
	ws.dirs.showDiff = true
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	tt.Frame()
	if !tt.HasText("查看 diff") {
		t.Error("dir-opened diff should show 查看diff checkbox")
	}

	// diff 列表入口的 diff：没有 checkbox（开关不管辖）
	ws.openDiff(ws.changes[0])
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	tt.Frame()
	if tt.HasText("查看 diff") {
		t.Error("changes-tab diff should not show 查看diff checkbox")
	}
}

// 翻转「查看 diff」时已打开的主区内容跟着切换：勾选把目录列表打开的只读
// 文件切到 diff；取消把目录入口打开的 diff 切回只读查看；diff 列表入口的
// diff 不受开关管辖，保持不动。
func TestDirShowDiffRetarget(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\nv3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()

	// 默认只读打开带改动文件，勾选后主区切到 diff（全展开）
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")
	tt.Frame() // checkbox 在内容头部：主区打开后重建帧才能点击
	if err := tt.Click("查看 diff"); err != nil {
		t.Fatalf("click 查看diff: %v", err)
	}
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.open == nil || ws.dirs.file != nil || !ws.diffv.ed.ExpandUnchanged || !ws.diffv.fromDir {
		t.Errorf("checking 查看diff should retarget file to diff: diff=%v file=%v expand=%v fromDir=%v",
			ws.diffv.open != nil, ws.dirs.file != nil, ws.diffv.ed.ExpandUnchanged, ws.diffv.fromDir)
	}

	// 取消勾选后目录入口的 diff 切回只读查看
	if err := tt.Click("查看 diff"); err != nil {
		t.Fatalf("click 查看diff again: %v", err)
	}
	if ws.diffv.open != nil || ws.dirs.file == nil {
		t.Errorf("unchecking 查看diff should retarget diff to file: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}

	// diff 列表入口打开的 diff 不受开关管辖：头部没有 checkbox，无从翻转
	ws.dirs.showDiff = true
	ws.openDiff(ws.changes[0])
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.fromDir {
		t.Fatal("changes-tab diff should not be marked dir-opened")
	}
	tt.Frame()
	if tt.HasText("查看 diff") {
		t.Error("changes-tab diff should not show 查看diff checkbox")
	}
	if ws.diffv.open == nil || ws.dirs.file != nil {
		t.Errorf("changes-tab diff should stay open: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
}
