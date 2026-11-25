package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"zhimareview/internal/git"
	"zhimareview/internal/store"
)

// diff 查看偏好是全局配置（config.json）：统一/分栏、展开未修改、查看 diff
// 各只有一个值，跨 tab、跨重启生效；只有显式切换才写盘。

// diffFixture 建带两个已修改文件的仓库。
func diffFixture(t *testing.T) string {
	t.Helper()
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/a.txt", "a\n")
	write(t, root+"/b.txt", "b\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	write(t, root+"/a.txt", "a\nA\n")
	write(t, root+"/b.txt", "b\nB\n")
	return root
}

// changeAt 取指定路径的改动（夹具只有未暂存改动，一节一条）。
func changeAt(t *testing.T, ws *workspace, path string) git.Change {
	t.Helper()
	for i := range ws.changes {
		if ws.changes[i].Path == path {
			return ws.changes[i]
		}
	}
	t.Fatalf("changes = %+v, want %s", ws.changes, path)
	return git.Change{}
}

// scanWorkspace 让 ws 进入 root 工作区态并完成一次扫描。
func scanWorkspace(t *testing.T, ws *workspace, root string) {
	t.Helper()
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
}

// 三个偏好都是全局的：显式切换即落盘；重启后打开任意文件都按同一份偏好。
func TestDiffViewPrefsPersistAcrossRestart(t *testing.T) {
	root := diffFixture(t)
	userDir := t.TempDir()
	a := newApplication(userDir)
	ws := a.activeTab()
	scanWorkspace(t, ws, root)

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	// 显式切分栏、展开
	ws.openDiff(changeAt(t, ws, "a.txt"))
	waitDiffLoaded(t, ws)
	tt.Frame()
	if err := tt.Click("分栏"); err != nil {
		t.Fatalf("click 分栏: %v", err)
	}
	if err := tt.Click("展开未修改"); err != nil {
		t.Fatalf("click 展开未修改: %v", err)
	}

	// 显式勾选「查看 diff」：在只读文件视图上点开关
	ws.openFile(root + "/a.txt")
	waitFileLoaded(t, ws)
	tt.Frame()
	if err := tt.Click("查看 diff"); err != nil {
		t.Fatalf("click 查看 diff: %v", err)
	}
	waitDiffLoaded(t, ws)

	cfg := store.LoadConfig(userDir)
	if cfg.Diff.Layout != 1 || !cfg.Diff.Expand || !cfg.Diff.ShowDiff {
		t.Fatalf("persisted diff prefs = %+v, want split/expanded/showDiff", cfg.Diff)
	}

	// 重启：新 application 读回同一份配置；换文件打开同样生效
	a2 := newApplication(userDir)
	ws2 := a2.activeTab()
	scanWorkspace(t, ws2, root)
	ws2.openDiff(changeAt(t, ws2, "b.txt"))
	waitDiffLoaded(t, ws2)
	tt2 := ui.NewTester(a2.view, 1180, 800)
	tt2.Frame()
	if !ws2.diffv.expand || ws2.diffv.ed.Layout != editor.DiffSplit {
		t.Errorf("restored view = expand:%v layout:%v, want expanded/split", ws2.diffv.expand, ws2.diffv.ed.Layout)
	}
	// 「查看 diff」也是全局的：目录入口的带改动文件直接进 diff
	ws2.openFile(root + "/b.txt")
	waitDiffLoaded(t, ws2)
	if ws2.diffv.open == nil || !ws2.diffv.fromDir {
		t.Errorf("dir entry should follow global showDiff: %+v", ws2.diffv.open)
	}
}

// 打开内容只读配置，不写盘：没有显式操作时 config.json 不应出现。
func TestOpeningContentDoesNotWriteConfig(t *testing.T) {
	root := diffFixture(t)
	userDir := t.TempDir()
	a := newApplication(userDir)
	ws := a.activeTab()
	scanWorkspace(t, ws, root)

	ws.openDiff(changeAt(t, ws, "a.txt"))
	waitDiffLoaded(t, ws)
	ws.openFile(root + "/b.txt")
	waitFileLoaded(t, ws)

	if _, err := os.Stat(filepath.Join(userDir, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("opening content wrote config: err=%v", err)
	}
}

// 提交历史详情的统一/分栏与主 diff 共用全局偏好：渲染按全局值，切换即写盘。
func TestHistoryLayoutUsesGlobalPref(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/base.txt", "base\nchanged\n")
	gitRun(t, root, "commit", "-am", "change")
	commits, err := git.LogCommits(root, 0, 1)
	if err != nil || len(commits) == 0 {
		t.Fatalf("LogCommits = %v, %v", commits, err)
	}
	patch, err := git.CommitPatch(root, commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}

	userDir := t.TempDir()
	a := newApplication(userDir)
	ws := a.activeTab()
	ws.mode = "work"
	ws.root = root
	ws.activeRepo = root
	ws.mainTab = 1
	ws.hist.all = commits
	ws.hist.commits = commits
	ws.hist.allLoaded = true
	ws.hist.done = true
	ws.hist.sel = 0
	ws.hist.detailText = patch
	ws.setCommitPatch(patch)
	if len(ws.hist.diffs) == 0 {
		t.Fatal("history detail has no diffs")
	}

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	if got := ws.hist.diffs[0].Layout; got != editor.DiffUnified {
		t.Fatalf("initial history layout = %v, want unified", got)
	}

	// 历史页切换分栏：全局配置更新、落盘，详情渲染跟随
	if err := tt.Click("分栏"); err != nil {
		t.Fatalf("click 分栏 in history: %v", err)
	}
	tt.Frame()
	if ws.diffPrefs().Layout != 1 {
		t.Fatalf("global layout = %d, want split", ws.diffPrefs().Layout)
	}
	if got := ws.hist.diffs[0].Layout; got != editor.DiffSplit {
		t.Errorf("history layout = %v, want split", got)
	}
	if cfg := store.LoadConfig(userDir); cfg.Diff.Layout != 1 {
		t.Errorf("persisted layout = %d, want split", cfg.Diff.Layout)
	}
}

// 全局偏好跨 tab 共享：一个 tab 里切换，另一个 tab 立即读到同一个值。
func TestDiffPrefsSharedAcrossTabs(t *testing.T) {
	a := restoreApplication(nil)
	other := a.newTabWorkspace()
	a.tabs = append(a.tabs, other)
	ws := a.tabs[0]
	ws.diffPrefs().Layout = 1
	ws.diffPrefs().Expand = true
	ws.diffPrefs().ShowDiff = true
	if other.diffPrefs().Layout != 1 || !other.diffPrefs().Expand || !other.diffPrefs().ShowDiff {
		t.Errorf("prefs not shared across tabs: %+v", *other.diffPrefs())
	}
}
