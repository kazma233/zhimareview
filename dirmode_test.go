package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// gitCmd 在 dir 里跑一条 git 命令（测试夹具用）。
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// write 创建带内容的文件（自动建父目录）。
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readDirEntries：目录在前文件在后、各自排序、symlink 与 .git 跳过。
func TestReadDirEntries(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	write(t, root+"/zdir/inner.txt", "x")
	write(t, root+"/afile.txt", "x")
	write(t, root+"/mfile.txt", "x")
	write(t, root+"/.git/HEAD", "x")
	write(t, root+"/nested/.git/HEAD", "x")
	if err := os.Symlink(root+"/missing", root+"/broken-link"); err == nil {
		// Windows 无特权时 symlink 可能失败；成功则验证跳过
		t.Cleanup(func() { os.Remove(root + "/broken-link") })
	}

	entries := readDirEntries(root)
	if len(entries) == 0 {
		t.Fatal("entries empty")
	}
	// 目录在前
	if !entries[0].folder {
		t.Errorf("entries[0] = %+v, want folder first", entries[0])
	}
	for i, e := range entries {
		if i > 0 && e.folder && !entries[i-1].folder {
			t.Errorf("file %q before folder %q", entries[i-1].name, e.name)
		}
		if e.name == "broken-link" {
			t.Error("symlink should be skipped")
		}
		if e.name == ".git" {
			t.Error(".git should be skipped")
		}
	}
	// 嵌套层的 .git 同样过滤
	for _, e := range readDirEntries(root + "/nested") {
		if e.name == ".git" {
			t.Error("nested .git should be skipped")
		}
	}
}

// rebuildDirRows：目录聚合最重状态与数量；展开后子层出现。
func TestRebuildDirRows(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	write(t, root+"/demo/src/app.go", "x")
	write(t, root+"/loose.txt", "x")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.dirs.cache = map[string][]fsEntry{root: readDirEntries(root)}
	ws.changes = []git.Change{
		{RepoPath: root + "/demo", RepoRel: "demo", Path: "src/app.go", Section: "unstaged", Code: "M"},
	}
	ws.dirs.rebuildRows(ws.root, ws.changes)

	// 未展开：根层两行（demo 目录 + loose.txt），demo 聚合 M(4)×1
	if len(ws.dirs.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(ws.dirs.rows))
	}
	if ws.dirs.rows[0].label != "demo" || ws.dirs.rows[0].rank != 4 || ws.dirs.rows[0].count != 1 {
		t.Errorf("demo row = %+v, want rank 4 count 1", ws.dirs.rows[0])
	}
	// 展开目录后子层出现
	ws.dirs.cache[root+"/demo"] = readDirEntries(root + "/demo")
	ws.dirs.cache[root+"/demo/src"] = readDirEntries(root + "/demo/src")
	ws.dirs.expanded = map[string]bool{root + "/demo": true, root + "/demo/src": true}
	ws.dirs.rebuildRows(ws.root, ws.changes)
	if len(ws.dirs.rows) != 4 {
		t.Fatalf("rows after expand = %d, want 4 (demo/src/app.go/loose)", len(ws.dirs.rows))
	}
	// 文件行 app.go 带状态色 rank
	found := false
	for _, r := range ws.dirs.rows {
		if r.label == "app.go" {
			found = true
			if r.rank != 4 {
				t.Errorf("app.go rank = %d, want 4", r.rank)
			}
		}
	}
	if !found {
		t.Error("app.go row missing")
	}
}

// 目录树行点击契约：点目录行开合，已选中的行也不例外——再点一次必须收起。
// List 的 Changed 只在「选择变化」时给信号，已选中行的重复点击是 no-op；
// 目录行是瞬时开合动作，点完让选区归 -1 才能让下一次点击仍是新选择。
func TestDirTreeRowToggle(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/demo/src/app.go", "x")
	write(t, root+"/loose.txt", "x")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))

	tt := ui.NewTester(ws.view, 300, 600)
	tt.Frame()
	tt.Frame()

	// 切到目录模式
	if err := tt.Click("目录列表"); err != nil {
		t.Fatalf("click dir tab: %v", err)
	}
	tt.Frame()
	tt.Frame()

	// 点目录行展开，子层出现
	if err := tt.Click("demo"); err != nil {
		t.Fatalf("click demo row: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("src") {
		t.Fatalf("first click did not expand: %v", tt.Texts())
	}

	// 再点同一行 → 收起
	if err := tt.Click("demo"); err != nil {
		t.Fatalf("click demo row again: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("src") {
		t.Fatal("second click did not collapse")
	}
}

func TestParentDir(t *testing.T) {
	if got := parentDir("D:/a/b/c"); got != "D:/a/b" {
		t.Errorf("parentDir = %q", got)
	}
	if got := parentDir("D:/a"); got != "D:" {
		t.Errorf("parentDir drive = %q", got)
	}
	if got := parentDir("/root"); got != "" {
		t.Errorf("parentDir posix root = %q, want empty", got)
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
	gitCmd(t, root, "add", ".")
	gitCmd(t, root, "commit", "-m", "base")
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
	gitCmd(t, root, "add", ".")
	gitCmd(t, root, "commit", "-m", "base")
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
	gitCmd(t, root, "add", ".")
	gitCmd(t, root, "commit", "-m", "base")
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

// buildChangeByAbs：同文件多分区只留工作区侧（unstaged > staged），
// 无改动文件不进索引；查表结果与 rebuildDerived 同步重建。
func TestBuildChangeByAbs(t *testing.T) {
	changes := []git.Change{
		{RepoPath: "/r", Path: "a.txt", Section: "staged", Code: "M"},
		{RepoPath: "/r", Path: "a.txt", Section: "unstaged", Code: "M"},
		{RepoPath: "/r", Path: "b.txt", Section: "staged", Code: "A"},
	}
	idx := buildChangeByAbs(changes)
	if got := idx["/r/a.txt"]; got == nil || got.Section != "unstaged" {
		t.Errorf("a.txt = %+v, want worktree (unstaged) side", got)
	}
	if got := idx["/r/b.txt"]; got == nil || got.Section != "staged" {
		t.Errorf("b.txt = %+v, want its only staged entry", got)
	}
	if _, ok := idx["/r/missing.txt"]; ok {
		t.Error("unchanged file should not be indexed")
	}
}

// staged 改名的 diff 两侧：旧侧按 HEAD 旧路径取，新侧按现路径取——实抓：
// 旧侧按现路径找 HEAD 对象必然不存在，改名 diff 打开只剩新侧（旧侧空白）。
func TestLoadSidesStagedRename(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/aidocs/note.md", "moved\n")
	gitCmd(t, root, "add", ".")
	gitCmd(t, root, "commit", "-m", "base")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "mv", "aidocs/note.md", "docs/note.md")

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
