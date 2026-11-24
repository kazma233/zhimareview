package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// diff 流程：真仓库夹具 → 打开 diff → 头部与两侧内容 → 还原 hunk 落盘。
func TestDiffViewAndRevert(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	// 修改基线文件产生 unstaged 改动
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\nchanged\n"), 0o644); err != nil {
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
	change := ws.changes[0]

	// 打开 diff（headless 下 goroutine 同步完成）
	ws.openDiff(change)
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")

	tt := ui.NewTester(ws.view, 1180, 800)
	if !tt.HasText("base.txt") {
		t.Errorf("texts = %q, want diff header path", tt.Texts())
	}
	// 编辑器行文本不经 a11y Text 元素暴露（官方测试同样只查分隔条/头文案），
	// 内容正确性由缓冲派生的 hunks 断言
	hunks := ws.diffv.ed.Hunks()
	if len(hunks) != 1 || hunks[0].NewStart != 1 || hunks[0].NewEnd != 2 {
		t.Fatalf("hunks = %+v, want one changed line at new line 1", hunks)
	}

	// 还原唯一 hunk → 文件回到基线内容
	ws.revertDiffHunk(hunks[0])
	waitFor(t, func() bool {
		data, err := os.ReadFile(filepath.Join(root, "base.txt"))
		return err == nil && string(data) == "base\n"
	}, 5*time.Second, "revert did not write baseline content")
}

// 暂存切换：diff 头部按钮后改动进入 staged 区。
func TestStageToggle(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\nnew\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	change := ws.changes[0]

	// 暂存 → staged 区出现该文件
	ws.toggleStageFile(change)
	waitFor(t, func() bool {
		st, err := git.ReadStatus(root)
		if err == nil && len(st.Staged) == 1 {
			return true
		}
		t.Logf("polling: err=%v errText=%q status=%+v", err, ws.errText(), st)
		return false
	}, 5*time.Second, "stage did not apply")

	// 打开中的 diff 分区跟随切换：生产流里头部按钮属于当前打开会话，
	// 暂存完成后 open 已随 flip 进入 staged（传与会话一致的值）
	staged := change
	staged.Section = "staged"
	ws.diffv.open = &staged
	ws.toggleStageFile(staged)
	waitFor(t, func() bool {
		st, err := git.ReadStatus(root)
		if err == nil && len(st.Staged) == 0 && len(st.Unstaged) == 1 {
			return true
		}
		t.Logf("polling: err=%v errText=%q status=%+v", err, ws.errText(), st)
		return false
	}, 5*time.Second, "unstage did not apply")
	if ws.diffv.open.Section != "unstaged" {
		t.Errorf("open diff section = %q, want unstaged after unstage", ws.diffv.open.Section)
	}
}

// diffPane 打开会话：applyDiffState 旧世代结果丢弃、当前世代落地；
// flip 同会话内翻转分区且世代不变。
func TestApplyDiffStateSession(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	v1 := git.Change{RepoPath: "D:/repos/a", Path: "f.txt", Section: "unstaged", Code: "M"}
	v2 := git.Change{RepoPath: "D:/repos/a", Path: "g.txt", Section: "staged", Code: "M"}
	ws.diffv.open = &v1
	ws.diffv.seq = 1

	ws.applyDiffState(2, v2, &diffSides{newValue: "x"}) // 旧世代（已换开别的 diff）
	if ws.diffv.sides != nil {
		t.Fatal("stale-seq result must be discarded")
	}
	ws.applyDiffState(1, v1, &diffSides{newValue: "x"}) // 当前世代
	if ws.diffv.sides == nil || ws.diffv.sides.newValue != "x" {
		t.Fatal("current-seq result must apply")
	}

	before := ws.diffv.seq
	ws.diffv.flip()
	if ws.diffv.open.Section != "staged" {
		t.Errorf("flip section = %q, want staged", ws.diffv.open.Section)
	}
	if ws.diffv.seq != before {
		t.Error("flip must not advance the session")
	}
}

// diff 头部展开/收起图标开关：diff 列表打开默认收起（标签「展开未修改」），
// 点击全展开（ExpandUnchanged 置位，标签变「收起未修改」），再点收起还原。
func TestDiffExpandToggle(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("one\nchanged\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	ws.openDiff(ws.changes[0]) // diff 列表入口：默认收起
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.ed.ExpandUnchanged {
		t.Fatal("changes-tree diff should default collapsed")
	}

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.Click("展开未修改"); err != nil {
		t.Fatalf("click expand-unchanged: %v", err)
	}
	tt.Frame()
	if !ws.diffv.ed.ExpandUnchanged {
		t.Error("clicking 展开未修改 did not set ExpandUnchanged")
	}
	// 展开后图标语义翻转，可访问标签随之变为「收起未修改」
	if err := tt.Click("收起未修改"); err != nil {
		t.Fatalf("click collapse-unchanged: %v", err)
	}
	tt.Frame()
	if ws.diffv.ed.ExpandUnchanged {
		t.Error("clicking 收起未修改 did not reset ExpandUnchanged")
	}
}

// staged 改名的 diff 两侧：旧侧按 HEAD 旧路径取，新侧按现路径取——实抓：
// 旧侧按现路径找 HEAD 对象必然不存在，改名 diff 打开只剩新侧（旧侧空白）。
func TestLoadSidesStagedRename(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/aidocs/note.md", "moved\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "mv", "aidocs/note.md", "docs/note.md")

	repos := git.FindRepos(root)
	changes := git.CollectChanges(repos)
	if len(changes) != 1 || changes[0].Code != "R" {
		t.Fatalf("changes = %+v, want 1 staged rename", changes)
	}
	sides := loadSides(changes[0])
	if sides.binary || sides.oldValue != "moved\n" || sides.newValue != "moved\n" {
		t.Fatalf("sides = old %q new %q binary %v, want both sides moved",
			sides.oldValue, sides.newValue, sides.binary)
	}
}
