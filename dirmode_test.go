package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

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
