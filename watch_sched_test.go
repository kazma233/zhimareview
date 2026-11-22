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

// watch 重读内容没变：编辑器状态（滚动所在）、Markdown 预览模式与已应用
// 内容都不动，也不经过 loading（实抓：重载走了完整重开，同目录别的文件
// 一保存，看着的文件就闪加载、滚回顶部、预览被踢回源码）。
func TestHandleFsEventsReloadFileKeepsState(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	write(t, root+"/sub/a.md", "# 标题\n")
	write(t, root+"/sub/b.md", "# 另一个\n")
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.openFile(root + "/sub/a.md")
	ws.mdPreview = true
	ed := ws.dirs.file.ed

	// 同目录另一个文件原样保存：目录级 hint 命中已打开文件
	write(t, root+"/sub/b.md", "# 另一个\n")
	ws.handleFsEvents(root, []string{"sub"})

	if ws.dirs.file == nil || ws.dirs.file.ed != ed {
		t.Fatal("editor state rebuilt on unchanged reload")
	}
	if ws.dirs.file.loading {
		t.Fatal("unchanged reload went through loading state")
	}
	if ws.dirs.file.content != "# 标题\n" {
		t.Fatalf("content = %q, want unchanged", ws.dirs.file.content)
	}
	if !ws.mdPreview {
		t.Fatal("markdown preview reset by unchanged reload")
	}
}

// watch 重读 diff 两侧没变：sides 与缓冲（滚动、展开态所在）不动，也不
// 经过 loading。
func TestHandleFsEventsReloadDiffKeepsState(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/changed.txt", "v1\n")
	gitCmd(t, root, "add", ".")
	gitCmd(t, root, "commit", "-m", "base")
	write(t, root+"/changed.txt", "v1\nv2\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	if len(ws.changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(ws.changes))
	}
	ws.openDiff(ws.changes[0])
	if ws.diffv.sides == nil {
		t.Fatal("diff not loaded")
	}
	sides, buf := ws.diffv.sides, ws.diffv.ed.New

	// 原样重存（编辑器保存未改内容）：hint 命中 → 重读 → 内容相同丢弃
	write(t, root+"/changed.txt", "v1\nv2\n")
	ws.handleFsEvents(root, []string{"."})

	if ws.diffv.sides != sides {
		t.Fatal("sides replaced on unchanged reload")
	}
	if ws.diffv.ed.New != buf {
		t.Fatal("diff buffers rebuilt on unchanged reload")
	}
	if ws.diffv.loading {
		t.Fatal("unchanged reload went through loading state")
	}
}

// watch 重读 diff 两侧变化：缓冲重建、模型更新为新内容。
func TestHandleFsEventsReloadDiffAppliesChange(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	write(t, root+"/changed.txt", "v1\n")
	gitCmd(t, root, "add", ".")
	gitCmd(t, root, "commit", "-m", "base")
	write(t, root+"/changed.txt", "v1\nv2\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	ws.openDiff(ws.changes[0])
	buf := ws.diffv.ed.New

	write(t, root+"/changed.txt", "v1\nv2\nv3\n")
	ws.handleFsEvents(root, []string{"."})

	if ws.diffv.ed.New == buf {
		t.Fatal("diff buffers not rebuilt on changed content")
	}
	if ws.diffv.sides.newValue != "v1\nv2\nv3\n" {
		t.Fatalf("newValue = %q, want updated", ws.diffv.sides.newValue)
	}
}

// 目录模式：目录级 hint 命中已加载目录 → 重读缓存并重建行（新增文件入行）。
func TestHandleFsEventsReloadDirs(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	// 与 readDirEntries 的条目链同风格（root 派生的 / 分隔）：filepath.Join
	// 在 Windows 产出反斜杠，expanded/cache 的 key 会和行 id 分裂，open 恒 false
	sub := root + "/sub"
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
