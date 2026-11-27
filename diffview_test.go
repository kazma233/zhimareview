package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// pngBytes 生成一张 2×2 纯色 PNG（图片夹具）：不同色值内容可区分。
func pngBytes(t *testing.T, r, g, b uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writePNG 在 root 下写一张图片文件（夹具）。
func writePNG(t *testing.T, root, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

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

// 重扫落地核对打开中的 diff：文件仍在列表时会话保持（滚动/展开/缓冲属于
// 已打开的内容），文件从列表消失（外部还原/提交）时关闭，主区回到
// 未选择文件的空态。
func TestRescanReconcilesOpenDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/base.txt", "base\nchanged\n")
	write(t, root+"/keep.txt", "keep\n") // 未跟踪改动，列表保持非空

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	var target *git.Change
	for i := range ws.changes {
		if ws.changes[i].Path == "base.txt" {
			target = &ws.changes[i]
		}
	}
	if target == nil {
		t.Fatalf("changes = %+v, want base.txt", ws.changes)
	}
	ws.openDiff(*target)
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	seq := ws.diffv.seq

	// 原样重扫：文件仍在列表，会话不被重开
	ws.rescan(root)
	if ws.diffv.open == nil || ws.diffv.open.Path != "base.txt" {
		t.Fatalf("open diff = %+v, want base.txt kept", ws.diffv.open)
	}
	if ws.diffv.seq != seq || ws.diffv.sides == nil {
		t.Fatal("unchanged rescan must not restart the open session")
	}

	// 外部还原 → 重扫后条目消失 → 会话关闭，主区回到空态
	gitRun(t, root, "checkout", "--", "base.txt")
	ws.rescan(root)
	if ws.diffv.open != nil {
		t.Fatalf("open diff = %+v, want closed after change disappeared", ws.diffv.open)
	}
	if len(ws.changes) != 1 || ws.changes[0].Path != "keep.txt" {
		t.Fatalf("changes = %+v, want only keep.txt", ws.changes)
	}
	tt := ui.NewTester(ws.view, 1180, 800)
	if !tt.HasText("点击左侧文件查看") {
		t.Errorf("texts = %q, want empty state of no selected file", tt.Texts())
	}
}

// 目录入口打开的 diff 改动消失：文件仍在磁盘上时回落只读查看（用户从
// 目录树来），文件已被删除则回到未选择空态。
func TestGoneDirDiffFallsBackToFile(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/base.txt", "base\nchanged\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	ws.diffPrefs().ShowDiff = true

	ws.openFile(root + "/base.txt") // 目录入口：查看 diff 勾选 → 直接进 diff
	waitDiffLoaded(t, ws)
	if ws.diffv.open == nil || !ws.diffv.fromDir {
		t.Fatalf("dir entry should open diff: %+v", ws.diffv.open)
	}

	// 外部还原：改动消失但文件还在 → 回落只读查看
	gitRun(t, root, "checkout", "--", "base.txt")
	ws.rescan(root)
	if ws.diffv.open != nil {
		t.Fatalf("open diff = %+v, want closed", ws.diffv.open)
	}
	if ws.dirs.file == nil || ws.dirs.file.abs != root+"/base.txt" {
		t.Fatalf("main content = %+v, want read-only file view of base.txt", ws.dirs.file)
	}
	waitFileLoaded(t, ws)

	// 未跟踪文件被删除：改动与文件都消失 → 回到空态
	write(t, root+"/temp.txt", "t\n")
	ws.rescan(root)
	ws.openDiffFromDir(changeAt(t, ws, "temp.txt"))
	waitDiffLoaded(t, ws)
	if err := os.Remove(root + "/temp.txt"); err != nil {
		t.Fatal(err)
	}
	ws.rescan(root)
	if ws.diffv.open != nil || ws.dirs.file != nil {
		t.Fatalf("deleted dir file should land empty: diff=%v file=%v",
			ws.diffv.open != nil, ws.dirs.file != nil)
	}
}

// 文件仍在列表但分区被外部改变（终端里 git add / git reset）：打开会话
// 对齐到新条目——分区徽标、写能力与两侧内容一起跟随。
func TestExternalStageRepointsOpenDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/base.txt", "base\nchanged\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	if len(ws.changes) != 1 || ws.changes[0].Section != "unstaged" {
		t.Fatalf("changes = %+v, want one unstaged entry", ws.changes)
	}
	ws.openDiff(ws.changes[0])
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if !ws.diffv.ed.HunkActions {
		t.Fatal("unstaged diff should offer hunk actions")
	}

	// 外部暂存 → 重扫对齐到 staged 行：徽标与只读能力跟随
	gitRun(t, root, "add", "base.txt")
	ws.rescan(root)
	if ws.diffv.open == nil || ws.diffv.open.Section != "staged" {
		t.Fatalf("open diff = %+v, want repointed to staged", ws.diffv.open)
	}
	if ws.diffv.ed.HunkActions {
		t.Error("staged diff must be read-only (no hunk actions)")
	}
	if tt := ui.NewTester(ws.view, 1180, 800); !tt.HasText("已暂存") {
		t.Errorf("texts = %q, want staged header", tt.Texts())
	}

	// 外部取消暂存 → 回到 unstaged 行；两侧文本与暂存前相同，写能力仍须
	// 恢复（内容比对早退不能带走分区派生的能力）
	gitRun(t, root, "reset", "-q", "HEAD", "--", "base.txt")
	ws.rescan(root)
	if ws.diffv.open == nil || ws.diffv.open.Section != "unstaged" {
		t.Fatalf("open diff = %+v, want repointed back to unstaged", ws.diffv.open)
	}
	if !ws.diffv.ed.HunkActions {
		t.Error("unstaged diff must offer hunk actions again")
	}
	if tt := ui.NewTester(ws.view, 1180, 800); !tt.HasText("工作区改动") {
		t.Errorf("texts = %q, want unstaged header", tt.Texts())
	}
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

// 打开文本 diff 焦点进官方编辑器：方向键直接移动光标，无需先点一下
//（DiffState 无聚焦查询 API，用光标行为断言——滚动受视口余量影响不稳）。
func TestDiffTextFocusesEditor(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/base.txt", "changed\n"+strings.Repeat("pad\n", 40))

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	ws.openDiff(ws.changes[0])
	waitDiffLoaded(t, ws)

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	cursor0 := ws.diffv.ed.Cursor()
	for i := 0; i < 5; i++ {
		tt.Key(0, ui.KeyDown)
	}
	if ws.diffv.ed.Cursor() == cursor0 {
		t.Fatal("arrow keys never reached the editor: focus did not land on open")
	}
	// 焦点被移走后，同内容重读不得抢回。编辑器标签随系统语言（Code
	// editor），先用「Tab 后方向键不再推动光标」自证焦点确实离开了
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	moved := ws.diffv.ed.Cursor()
	tt.Key(0, ui.KeyDown)
	if ws.diffv.ed.Cursor() != moved {
		t.Skip("Tab did not move focus away in this view; reload-steal not observable")
	}
	cursor1 := ws.diffv.ed.Cursor()
	ws.reloadDiff()
	tt.Frame()
	tt.Frame()
	for i := 0; i < 3; i++ {
		tt.Key(0, ui.KeyDown)
	}
	if ws.diffv.ed.Cursor() != cursor1 {
		t.Fatal("same-content reload stole the focus back to the editor")
	}
}

// 图片 diff 的两侧字节按分区语义取：unstaged 旧=index/新=工作区、staged
// 旧=HEAD/新=index（不是工作区字节——旧实现只读工作区，staged 显示错版本、
// 删除的图片完全看不到）、untracked 仅新侧。
func TestLoadSidesImageBytes(t *testing.T) {
	red, green, blue := pngBytes(t, 255, 0, 0), pngBytes(t, 0, 255, 0), pngBytes(t, 0, 0, 255)
	committed := func(t *testing.T, root string, data []byte) {
		t.Helper()
		writePNG(t, root, "pic.png", data)
		gitRun(t, root, "add", ".")
		gitRun(t, root, "commit", "-m", "base")
	}
	scan := func(t *testing.T, root string) []git.Change {
		t.Helper()
		changes := git.CollectChanges(git.FindRepos(root))
		if len(changes) != 1 {
			t.Fatalf("changes = %+v, want exactly one entry", changes)
		}
		return changes
	}

	t.Run("unstaged 修改", func(t *testing.T) {
		root := filepath.ToSlash(t.TempDir())
		gitInit(t, root)
		committed(t, root, red)
		writePNG(t, root, "pic.png", green)

		sides := loadSides(scan(t, root)[0])
		if !sides.binary || !bytes.Equal(sides.binOld, red) || !bytes.Equal(sides.binNew, green) {
			t.Fatalf("sides binary=%v old=%v new=%v, want old=red new=green",
				sides.binary, sides.binOld, sides.binNew)
		}
	})

	t.Run("staged 修改取 index 版本", func(t *testing.T) {
		root := filepath.ToSlash(t.TempDir())
		gitInit(t, root)
		committed(t, root, red)
		writePNG(t, root, "pic.png", green)
		gitRun(t, root, "add", "pic.png")
		writePNG(t, root, "pic.png", blue) // 工作区再改：staged diff 与工作区无关

		changes := git.CollectChanges(git.FindRepos(root))
		// 工作区再改后 staged 与 unstaged 两条并存，挑 staged 条目
		var staged *git.Change
		for i := range changes {
			if changes[i].Section == "staged" {
				staged = &changes[i]
			}
		}
		if staged == nil {
			t.Fatalf("changes = %+v, want a staged entry", changes)
		}
		sides := loadSides(*staged)
		if !sides.binary || !bytes.Equal(sides.binOld, red) || !bytes.Equal(sides.binNew, green) {
			t.Fatalf("sides binary=%v old=%v new=%v, want old=red new=index green (not worktree blue)",
				sides.binary, sides.binOld, sides.binNew)
		}
	})

	t.Run("unstaged 删除保留旧图", func(t *testing.T) {
		root := filepath.ToSlash(t.TempDir())
		gitInit(t, root)
		committed(t, root, red)
		if err := os.Remove(filepath.Join(root, "pic.png")); err != nil {
			t.Fatal(err)
		}

		sides := loadSides(scan(t, root)[0])
		if !sides.binary || !bytes.Equal(sides.binOld, red) || len(sides.binNew) != 0 {
			t.Fatalf("sides binary=%v old=%v new=%v, want old=red new empty",
				sides.binary, sides.binOld, sides.binNew)
		}
	})

	t.Run("untracked 仅新侧", func(t *testing.T) {
		root := filepath.ToSlash(t.TempDir())
		gitInit(t, root)
		writePNG(t, root, "pic.png", green)

		changes := scan(t, root)
		if changes[0].Section != "untracked" {
			t.Fatalf("changes = %+v, want one untracked entry", changes)
		}
		sides := loadSides(changes[0])
		if !sides.binary || len(sides.binOld) != 0 || !bytes.Equal(sides.binNew, green) {
			t.Fatalf("sides binary=%v old=%v new=%v, want old empty new=green",
				sides.binary, sides.binOld, sides.binNew)
		}
	})
}

// 图片 diff 的落地与渲染：applyDiffState 解码两侧 bitmap、sidesContentEqual
// 对两侧字节生效；渲染两侧出两列、删除只剩旧侧列、非图片二进制维持
// 「二进制文件」空态，且图片 diff 头部不渲染统一/分栏布局开关。
func TestDiffImageSidesRender(t *testing.T) {
	red, green := pngBytes(t, 255, 0, 0), pngBytes(t, 0, 255, 0)

	t.Run("解码与相等比对", func(t *testing.T) {
		ws := newWorkspace(nil)
		ws.mode = "work"
		v := git.Change{RepoPath: "D:/repos/a", Path: "pic.png", Section: "unstaged", Code: "M"}
		ws.diffv.open = &v
		ws.diffv.seq = 1
		ws.applyDiffState(1, v, &diffSides{binary: true, binOld: red, binNew: green})
		if !sidesContentEqual(ws.diffv.sides, &diffSides{binary: true, binOld: red, binNew: green}) {
			t.Fatal("identical binary sides must compare equal")
		}
		if sidesContentEqual(ws.diffv.sides, &diffSides{binary: true, binOld: green, binNew: green}) {
			t.Fatal("different binOld must compare unequal")
		}

		// 内容变化归位图片缩放；同内容重放（watch 重读）早退保持
		ws.diffv.imgZoom.scale = 3
		ws.applyDiffState(1, v, &diffSides{binary: true, binOld: green, binNew: red})
		if ws.diffv.imgZoom.scale != 1 {
			t.Fatal("content change must reset image zoom")
		}
		ws.diffv.imgZoom.scale = 3
		ws.applyDiffState(1, v, &diffSides{binary: true, binOld: green, binNew: red})
		if ws.diffv.imgZoom.scale != 3 {
			t.Fatal("same-content reapply must keep image zoom")
		}
	})

	t.Run("两侧并排", func(t *testing.T) {
		root := filepath.ToSlash(t.TempDir())
		gitInit(t, root)
		writePNG(t, root, "pic.png", red)
		gitRun(t, root, "add", ".")
		gitRun(t, root, "commit", "-m", "base")
		writePNG(t, root, "pic.png", green)

		ws := newWorkspace(nil)
		ws.mode = "work"
		ws.root = root
		repos := git.FindRepos(root)
		ws.applyScanState(root, repos, git.CollectChanges(repos))
		if len(ws.changes) != 1 {
			t.Fatalf("changes = %+v, want exactly one entry", ws.changes)
		}
		ws.openDiff(ws.changes[0])
		waitDiffLoaded(t, ws)

		tt := ui.NewTester(ws.view, 1180, 800)
		if !tt.HasText("旧版") || !tt.HasText("新版") {
			t.Errorf("texts = %q, want both side labels", tt.Texts())
		}
		if tt.HasText("二进制文件") {
			t.Errorf("texts = %q, binary empty state must be gone", tt.Texts())
		}
		if tt.HasText("分栏") {
			t.Errorf("texts = %q, layout switch must hide for image diff", tt.Texts())
		}
		// 尺寸·大小信息随列头展示（解码成功才有）
		sizeShown := false
		for _, s := range tt.Texts() {
			if strings.Contains(s, "×") && strings.Contains(s, "·") {
				sizeShown = true
			}
		}
		if !sizeShown {
			t.Errorf("texts = %q, want size · bytes info", tt.Texts())
		}
		// 滚轮落在任一列都缩放同一份状态（两列联动）：先在新版列、再在旧版列
		tt.Frame()
		tt.Frame()
		tt.Wheel(800, 400, 0, -40) // 新版列
		if ws.diffv.imgZoom.scale <= 1 {
			t.Errorf("wheel over the new pane should zoom the shared state, scale = %v", ws.diffv.imgZoom.scale)
		}
		mark := ws.diffv.imgZoom.scale
		tt.Wheel(400, 400, 0, -40) // 旧版列
		if ws.diffv.imgZoom.scale <= mark {
			t.Errorf("wheel over the old pane should zoom the shared state, scale %v -> %v", mark, ws.diffv.imgZoom.scale)
		}
		// 打开请求把焦点送进新版列：键盘无需点击即可缩放（联动状态）
		if !tt.Focused("新版图片") {
			t.Errorf("focus = %v, want the new-image pane focused on open", tt.Focused("新版图片"))
		}
		before := ws.diffv.imgZoom.scale
		tt.Key(0, ui.KeyEqual)
		if ws.diffv.imgZoom.scale <= before {
			t.Error("keyboard + should zoom the focused image pane")
		}
	})

	t.Run("删除仅旧侧", func(t *testing.T) {
		root := filepath.ToSlash(t.TempDir())
		gitInit(t, root)
		writePNG(t, root, "pic.png", red)
		gitRun(t, root, "add", ".")
		gitRun(t, root, "commit", "-m", "base")
		if err := os.Remove(filepath.Join(root, "pic.png")); err != nil {
			t.Fatal(err)
		}

		ws := newWorkspace(nil)
		ws.mode = "work"
		ws.root = root
		repos := git.FindRepos(root)
		ws.applyScanState(root, repos, git.CollectChanges(repos))
		if len(ws.changes) != 1 {
			t.Fatalf("changes = %+v, want exactly one entry", ws.changes)
		}
		ws.openDiff(ws.changes[0])
		waitDiffLoaded(t, ws)

		tt := ui.NewTester(ws.view, 1180, 800)
		if !tt.HasText("旧版") {
			t.Errorf("texts = %q, want old side label", tt.Texts())
		}
		if tt.HasText("新版") {
			t.Errorf("texts = %q, deleted image must not show new side", tt.Texts())
		}
		// 焦点 Handle 必须绑到实际渲染的旧版列：否则打开请求的聚焦永远
		// 悬空（只绑新版列的旧实现如此）
		tt.Frame()
		tt.Frame()
		if !tt.Focused("旧版图片") {
			t.Error("deleted-image diff must focus the old pane on open")
		}
	})

	t.Run("非图片二进制维持空态", func(t *testing.T) {
		root := filepath.ToSlash(t.TempDir())
		gitInit(t, root)
		write(t, root+"/data.bin", "v1\x00binary")
		gitRun(t, root, "add", ".")
		gitRun(t, root, "commit", "-m", "base")
		write(t, root+"/data.bin", "v2\x00changed")

		ws := newWorkspace(nil)
		ws.mode = "work"
		ws.root = root
		repos := git.FindRepos(root)
		ws.applyScanState(root, repos, git.CollectChanges(repos))
		if len(ws.changes) != 1 {
			t.Fatalf("changes = %+v, want exactly one entry", ws.changes)
		}
		ws.openDiff(ws.changes[0])
		waitDiffLoaded(t, ws)

		tt := ui.NewTester(ws.view, 1180, 800)
		if tt.HasText("旧版") || !tt.HasText("二进制文件") {
			t.Errorf("texts = %q, want binary empty state without side labels", tt.Texts())
		}
	})
}

// 旧侧对象路径统一按 OrigPath 取（与 staged 分支同口径）。porcelain v1
// 目前对工作区改名报 D + untracked 而非 R，所以这里用合成的 Change 固定
// 规则：status 口径变化或条目来源扩展时，旧侧不会凭空丢内容。
func TestLoadSidesOldPathFromOrigPath(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/a.txt", "moved\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	// 只在磁盘上改名（未暂存）：index 仍指旧路径
	if err := os.Rename(filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}

	text := loadSides(git.Change{RepoPath: root, Path: "b.txt", OrigPath: "a.txt", Section: "unstaged", Code: "R"})
	if text.oldValue != "moved\n" || text.newValue != "moved\n" {
		t.Fatalf("text sides = old %q new %q, want both from old path/index", text.oldValue, text.newValue)
	}
}
