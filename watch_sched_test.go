package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"zhimareview/internal/git"
)

// watchSched 合批：两次事件并入一批、一次 flush 全量取出后队列排干。
func TestWatchSchedBatchMerge(t *testing.T) {
	var s watchSched
	s.push([]string{"a.txt"})
	s.push([]string{"b.txt", "c.txt"})
	paths, ok := s.flush(time.Now())
	if !ok || len(paths) != 3 {
		t.Fatalf("flush = %v %v, want 3 paths ok", paths, ok)
	}
	if _, ok := s.flush(time.Now()); ok {
		t.Fatal("second flush should find no batch")
	}
}

// watchSched 限流：「.」全量批 1 秒内只放行一次（限流丢弃整批）；普通批
// 不受限流也不续延限流窗口——窗口只从最近放行的「.」批起算。
func TestWatchSchedFullFlushRateLimit(t *testing.T) {
	var s watchSched
	base := time.Unix(0, 0)
	s.push([]string{"."})
	if paths, ok := s.flush(base); !ok || len(paths) != 1 {
		t.Fatalf("first full flush = %v %v, want ok", paths, ok)
	}
	// 1 秒内的第二个「.」批：整批丢弃
	s.push([]string{"."})
	if _, ok := s.flush(base.Add(500 * time.Millisecond)); ok {
		t.Fatal("full flush within 1s should be dropped")
	}
	// 限流丢弃后普通批照常放行
	s.push([]string{"x.txt"})
	if paths, ok := s.flush(base.Add(800 * time.Millisecond)); !ok || len(paths) != 1 {
		t.Fatalf("normal batch = %v %v, want ok regardless of rate limit", paths, ok)
	}
	// 普通批放行不续延窗口：距首个「.」批 1.5s，放行
	s.push([]string{"."})
	if _, ok := s.flush(base.Add(1500 * time.Millisecond)); !ok {
		t.Fatal("full flush after 1s since last passed full flush should pass")
	}
}

// 事件命中打开中的文件 → 重读内容（headless 下管线同步落地）。
func TestHandleFsEventsReloadFile(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.openFile(root + "/note.txt")
	if ws.dirs.file == nil || ws.dirs.file.content != "v1\n" {
		t.Fatalf("open file state = %+v, want v1 loaded", ws.dirs.file)
	}

	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"note.txt"})
	if ws.dirs.file == nil || ws.dirs.file.content != "v2\n" {
		t.Fatalf("file did not reload on fs event: %+v", ws.dirs.file)
	}
}

// 目录模式：目录级 hint 命中已加载目录 → 重读缓存并重建行（新增文件入行）。
func TestHandleFsEventsReloadDirs(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "old.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.applyScanState(root, nil, nil)
	ws.sidebarTab = "dir"
	ws.loadDir(root)
	ws.toggleDir(sub) // 展开 → 缓存 sub 子项

	if err := os.WriteFile(filepath.Join(sub, "new.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	// HintsOnly 事件是目录级提示：hint 即目录相对路径
	ws.handleFsEvents(root, []string{"sub"})
	found := false
	for _, r := range ws.dirs.rows {
		if r.label == "new.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("new.txt not in rows after dir reload: %+v", ws.dirs.rows)
	}
}

// 仓库根下的文件级 hint 命中所在仓库 → 重采合并（headless 下同步落地）。
// 深层 hint 不能只靠「hint 覆盖目标」单方向匹配：仓库是被 hint 覆盖的一方。
func TestHandleFsEventsCollectDeepHint(t *testing.T) {
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
		t.Fatalf("changes = %d, want 1 unstaged entry", len(ws.changes))
	}

	// 工作区改回基线（变干净）→ 文件级 hint 触发重采，合并后条目清空
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"base.txt"})
	if len(ws.changes) != 0 {
		t.Fatalf("changes = %d after file-level hint, want merged to 0", len(ws.changes))
	}
}
