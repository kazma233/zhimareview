package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"zhimareview/internal/git"
)

// 选择态：入口文案、选目录按钮、最近目录列表。
func TestPickerView(t *testing.T) {
	ws := newWorkspace(nil)
	ws.recent = []string{"D:/repos", "D:/work"}
	tt := ui.NewTester(ws.view, 1180, 800)

	if !tt.HasText("选择目录…") {
		t.Errorf("texts = %q, want 选择目录… button", tt.Texts())
	}
	if !tt.HasText("D:/repos") || !tt.HasText("D:/work") {
		t.Errorf("texts = %q, want recent dirs listed", tt.Texts())
	}

	// 点击最近目录进入工作区：顶栏显示根路径
	if err := tt.Click("D:/repos"); err != nil {
		t.Fatal(err)
	}
	if ws.mode != "work" || ws.root != "D:/repos" {
		t.Errorf("mode = %q root = %q, want work / D:/repos", ws.mode, ws.root)
	}
	if !tt.HasText("D:/repos") {
		t.Errorf("texts = %q, want root in topbar", tt.Texts())
	}
}

// 「＋」新开 tab 的契约见 tabbar_test.go（原「新窗口」按钮已随多窗口模型
// 移除，工作区原地保留由 TestTabBarPlusOpensPick 覆盖）。

// 最近目录条目数上限由 store 层保证；这里验证空数据不渲染区块。
func TestPickerWithoutRecent(t *testing.T) {
	ws := newWorkspace(nil)
	tt := ui.NewTester(ws.view, 1180, 800)

	if tt.HasText("最近使用") {
		t.Errorf("texts = %q, want no recent section", tt.Texts())
	}
}

// 扫描结果应用：默认仓库落在第一个有改动的仓库；换目录后旧结果作废。
func TestApplyScanState(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	repos := []git.Repo{
		{Path: "D:/repos/demo", Name: "demo", Rel: "demo"},
		{Path: "D:/repos/clean", Name: "clean", Rel: "clean"},
	}
	changes := []git.Change{
		{RepoPath: "D:/repos/clean", RepoRel: "clean", Path: "f", Section: "unstaged", Code: "M"},
	}
	ws.applyScanState("D:/repos", repos, changes)

	if ws.activeRepo != "D:/repos/clean" {
		t.Errorf("activeRepo = %q, want the repo with changes", ws.activeRepo)
	}
	if len(ws.tree.roots) != 2 || ws.tree.flat == nil || len(ws.hist.repoOptions) != 2 {
		t.Errorf("derived data not rebuilt: roots=%d options=%d", len(ws.tree.roots), len(ws.hist.repoOptions))
	}
	// 扫描期间换目录：过期结果作废
	ws.root = "D:/other"
	ws.applyScanState("D:/repos", repos, nil)
	if len(ws.repos) != 2 {
		t.Errorf("stale scan applied after root change")
	}
}

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

// waitFor 轮询等待后台 goroutine 的效果落地（写盘、状态刷新）。
func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// gitInit 在 dir 建带基线提交（base.txt）的仓库（main 包测试的本地夹具）。
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@t"},
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
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

// 事件过滤：.git 元数据（目录与 worktree 的 .git 文件）被剔除，"." 全量提示保留。
func TestWatchEventPaths(t *testing.T) {
	events := []mygo.FileEvent{
		{Op: mygo.FileWrite, Path: "demo/src/app.go"},
		{Op: mygo.FileWrite, Path: "demo/.git/index"},
		{Op: mygo.FileWrite, Path: ".git"},
		{Op: mygo.FileWrite, Path: "wtree/.git"},
		{Op: mygo.FileRemove, Path: "loose.txt"},
		{Op: mygo.FileHint, Path: "."},
		{Op: mygo.FileWrite, Path: ""},
	}
	paths := watchEventPaths(events)
	want := []string{"demo/src/app.go", "loose.txt", "."}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

// hitHint：目录级提示命中其下所有路径；"." 全量命中。
func TestHitHint(t *testing.T) {
	root := "D:/repos"
	if !hitHint(root, "demo", root+"/demo/src/app.go") {
		t.Error("dir hint should hit file under it")
	}
	if !hitHint(root, "demo/src", root+"/demo/src/app.go") {
		t.Error("nested dir hint should hit file")
	}
	if hitHint(root, "demo", root+"/other/f.txt") {
		t.Error("hint should not hit sibling path")
	}
	if !hitHint(root, ".", root+"/anything") {
		t.Error("dot hint should hit everything")
	}
	if !hitHint(root, "demo", root+"/demo") {
		t.Error("hint should hit its own dir")
	}
	// 反向：事件细于目标（目标目录/仓库覆盖事件路径）
	if !hitHint(root, "demo/src/app.go", root+"/demo") {
		t.Error("file under target should hit the target")
	}
	if hitHint(root, "other/f.txt", root+"/demo") {
		t.Error("event outside target should not hit it")
	}
}

// mergeChanges：受影响仓库条目被替换，其余保持。
func TestMergeChanges(t *testing.T) {
	ws := newWorkspace(nil)
	ws.changes = []git.Change{
		{RepoPath: "/r/a", RepoRel: "a", Path: "f1", Section: "unstaged", Code: "M"},
		{RepoPath: "/r/b", RepoRel: "b", Path: "f2", Section: "untracked", Code: "?"},
	}
	fresh := []git.Change{
		{RepoPath: "/r/a", RepoRel: "a", Path: "f1", Section: "staged", Code: "M"},
	}
	ws.mergeChanges(fresh, map[string]bool{"/r/a": true})
	if len(ws.changes) != 2 {
		t.Fatalf("changes = %d, want 2", len(ws.changes))
	}
	if ws.changes[0].RepoPath != "/r/b" {
		t.Errorf("changes[0] = %+v, want kept b entry", ws.changes[0])
	}
	if ws.changes[1].Section != "staged" {
		t.Errorf("changes[1] = %+v, want fresh staged entry", ws.changes[1])
	}
}

// structureChanged：仓库集合增删感知，顺序无关。
func TestStructureChanged(t *testing.T) {
	ws := newWorkspace(nil)
	ws.repos = []git.Repo{{Rel: "a"}, {Rel: "b"}}
	if ws.structureChanged([]git.Repo{{Rel: "b"}, {Rel: "a"}}) {
		t.Error("same set should be unchanged")
	}
	if !ws.structureChanged([]git.Repo{{Rel: "b"}}) {
		t.Error("removal should be detected")
	}
	if !ws.structureChanged([]git.Repo{{Rel: "a"}, {Rel: "b"}, {Rel: "c"}}) {
		t.Error("addition should be detected")
	}
}

// 事件命中打开中的 diff：内容重载（headless 下 goroutine 同步落地）。
func TestHandleFsEventsReloadDiff(t *testing.T) {
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
	change := ws.changes[0]
	ws.openDiff(change)
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.sides.newValue != "base\nv2\n" {
		t.Fatalf("initial diff sides = %+v, want v2 content", ws.diffv.sides)
	}

	// 外部把文件改回基线 → 事件重载 diff
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"base.txt"})
	waitFor(t, func() bool {
		return ws.diffv.sides != nil && ws.diffv.sides.newValue == "base\n"
	}, 5*time.Second, "diff did not reload on fs event")
}

// 事件命中仓库 → 管线重采合并：受影响仓库条目被替换、派生行重建
// （headless 下同步落地；"." 是系统丢失变更后的全量提示）。
func TestHandleFsEventsCollectMerge(t *testing.T) {
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
	// 工作区改回基线（变干净）→ 全量事件触发重采，合并后条目清空
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"."})
	if len(ws.changes) != 0 {
		t.Fatalf("changes = %d after full-flush event, want merged to 0", len(ws.changes))
	}
	// 派生行同步重建：文件行清空，只剩空仓库的普通文字行（branch、无状态字母、计数 0）
	if len(ws.tree.rows) != 1 || !ws.tree.rows[0].branch ||
		ws.tree.rows[0].letter != "" || ws.tree.rows[0].count != 0 {
		t.Errorf("changeRows = %+v, want only the empty-repo plain row", ws.tree.rows)
	}
}

// 提交历史补丁解析：真实 git show 补丁 → 官方 ParsePatch 逐文件拆分。
func TestCommitPatchParse(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "two files"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commits, err := git.LogCommits(root, 0, 0)
	if err != nil || len(commits) < 2 {
		t.Fatalf("LogCommits = %v, %v", commits, err)
	}
	patch, err := git.CommitPatch(root, commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	files, err := editor.ParsePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	// 两个新增文件各成一段，名字不带 a// b/ 前缀
	if len(files) != 2 || files[0].NewName != "a.txt" || files[1].NewName != "b.txt" {
		t.Fatalf("files = %+v, want a.txt then b.txt", files)
	}
	for i, fp := range files {
		if fp.Binary || len(fp.Hunks) == 0 {
			t.Errorf("files[%d] = %+v, want text patch with hunks", i, fp)
		}
	}
	// 逐文件 diff 视图状态：hunk 行号来自补丁，可据此落盘还原
	ws := newWorkspace(nil)
	ws.setCommitPatch(patch)
	if len(ws.hist.files) != 2 || len(ws.hist.diffs) != 2 {
		t.Fatalf("commit state = %d files %d diffs, want 2/2", len(ws.hist.files), len(ws.hist.diffs))
	}
	h := ws.hist.diffs[0].Hunks()
	if len(h) != 1 || h[0].OldStart != 0 || h[0].OldEnd != 0 || h[0].NewStart != 0 || h[0].NewEnd != 1 {
		t.Errorf("a.txt hunks = %+v, want one addition of line 1", h)
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

// gitRun 在测试目录执行 git（夹具搭建失败即终止测试）。
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// cloneWithRemote 建本地 bare 远程与两个 clone：origin 用于推送新提交，
// work 是被测仓库（已跟踪 origin/main）。
func cloneWithRemote(t *testing.T) (origin, work string) {
	t.Helper()
	base := filepath.ToSlash(t.TempDir())
	remote := base + "/remote.git"
	seed := base + "/seed"
	gitRun(t, base, "init", "--bare", "-b", "main", remote)
	gitInit(t, seed)
	gitRun(t, seed, "remote", "add", "origin", remote)
	gitRun(t, seed, "push", "-u", "origin", "main")
	origin = base + "/origin"
	work = base + "/work"
	gitRun(t, base, "clone", remote, origin)
	gitRun(t, base, "clone", remote, work)
	for _, dir := range []string{origin, work} {
		gitRun(t, dir, "config", "user.name", "t")
		gitRun(t, dir, "config", "user.email", "t@t")
	}
	return origin, work
}

// pushCommit 在 origin 写文件并推到远程。
func pushCommit(t *testing.T, origin, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(origin, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "add", ".")
	gitRun(t, origin, "commit", "-m", message)
	gitRun(t, origin, "push")
}

// 历史页 fetch：远程新提交进入当前仓库的提交列表。
func TestFetchActiveRepoReloadsCommits(t *testing.T) {
	origin, work := cloneWithRemote(t)
	pushCommit(t, origin, "remote.txt", "remote\n", "remote work")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = work
	repos := git.FindRepos(work)
	ws.applyScanState(work, repos, git.CollectChanges(repos))
	if ws.activeRepo != work {
		t.Fatalf("activeRepo = %q, want %q", ws.activeRepo, work)
	}
	ws.mainTab = 1
	ws.loadCommits()
	if len(ws.hist.commits) != 1 || ws.hist.commits[0].Subject != "init" {
		t.Fatalf("commits = %+v, want only init before fetch", ws.hist.commits)
	}

	ws.fetchActiveRepo()
	if ws.hist.remoteBusy != "" {
		t.Errorf("remoteBusy = %q, want cleared", ws.hist.remoteBusy)
	}
	if ws.errText() != "" {
		t.Errorf("errText = %q, want empty", ws.errText())
	}
	if !hasSubject(ws.hist.commits, "remote work") || len(ws.hist.commits) != 2 {
		t.Errorf("commits = %+v, want fetched commit present", ws.hist.commits)
	}
}

// hasSubject 判断提交列表是否含指定标题（LogCommits 的排序在同秒提交间
// 不稳定，测试按集合断言）。
func hasSubject(rows []git.CommitRow, subject string) bool {
	for _, r := range rows {
		if r.Subject == subject {
			return true
		}
	}
	return false
}

// 历史页 pull：默认快进，工作区文件与提交列表同步更新。
func TestPullActiveRepoFastForwards(t *testing.T) {
	origin, work := cloneWithRemote(t)
	pushCommit(t, origin, "remote.txt", "remote\n", "remote work")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = work
	repos := git.FindRepos(work)
	ws.applyScanState(work, repos, git.CollectChanges(repos))
	ws.mainTab = 1
	ws.loadCommits()

	ws.pullActiveRepo(false)
	if ws.hist.remoteBusy != "" {
		t.Errorf("remoteBusy = %q, want cleared", ws.hist.remoteBusy)
	}
	data, err := os.ReadFile(filepath.Join(work, "remote.txt"))
	if err != nil || string(data) != "remote\n" {
		t.Errorf("remote.txt = %q, %v, want pulled content", data, err)
	}
	// pull 后的重扫：工作区仍然干净（新文件已随快进入库）
	if len(ws.changes) != 0 {
		t.Errorf("changes = %+v, want clean worktree after ff pull", ws.changes)
	}
	if len(ws.hist.commits) != 2 || !hasSubject(ws.hist.commits, "remote work") {
		t.Errorf("commits = %+v, want pulled commit present", ws.hist.commits)
	}
}

// 历史页 pull 失败：重扫工作区之后错误信息仍然保留（不被扫描成功清空）。
func TestPullActiveRepoFailureKeepsError(t *testing.T) {
	origin, work := cloneWithRemote(t)
	if err := os.WriteFile(filepath.Join(work, "local.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "add", ".")
	gitRun(t, work, "commit", "-m", "local work")
	pushCommit(t, origin, "remote.txt", "remote\n", "remote work")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = work
	repos := git.FindRepos(work)
	ws.applyScanState(work, repos, git.CollectChanges(repos))
	ws.mainTab = 1
	ws.loadCommits()

	ws.pullActiveRepo(false)
	if ws.hist.remoteBusy != "" {
		t.Errorf("remoteBusy = %q, want cleared", ws.hist.remoteBusy)
	}
	if !strings.Contains(ws.errText(), "pull 失败") {
		t.Errorf("errText = %q, want pull failure to survive the rescan", ws.errText())
	}
	// 分叉 pull 失败后本地工作区不被改动
	if _, err := os.Stat(filepath.Join(work, "remote.txt")); !os.IsNotExist(err) {
		t.Errorf("remote.txt should not exist after refused ff pull, err=%v", err)
	}
}

// 全部 fetch：无 remote 的仓库不报错，坏 remote 按仓库聚合反馈；
// 完成后忙碌标记清除且历史重载。
func TestFetchAllReposAggregatesErrors(t *testing.T) {
	_, work := cloneWithRemote(t)
	plain := filepath.ToSlash(t.TempDir()) + "/plain"
	gitInit(t, plain)
	bad := filepath.ToSlash(t.TempDir()) + "/bad"
	gitInit(t, bad)
	gitRun(t, bad, "remote", "add", "origin", bad+"/missing.git")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = filepath.ToSlash(t.TempDir())
	ws.repos = []git.Repo{
		{Path: work, Name: "work", Rel: "work"},
		{Path: plain, Name: "plain", Rel: "plain"},
		{Path: bad, Name: "bad", Rel: "bad"},
	}
	ws.activeRepo = work
	ws.mainTab = 1
	ws.loadCommits()

	ws.fetchAllRepos()
	if ws.fetchingAll {
		t.Error("fetchingAll must be cleared after completion")
	}
	if !strings.Contains(ws.errText(), "fetch 失败 1/3") || !strings.Contains(ws.errText(), "bad") {
		t.Errorf("errText = %q, want one named failure out of three", ws.errText())
	}
	if len(ws.hist.commits) == 0 {
		t.Error("commits must be reloaded after fetch all")
	}

	// 不在历史页时全部 fetch 也要让历史失效，进入页面时重新加载
	ws.mainTab = 0
	ws.hist.all = []git.CommitRow{{Hash: "stale"}}
	ws.fetchAllRepos()
	if ws.hist.all != nil {
		t.Error("fetch all off the history tab must invalidate commits")
	}
}

// 历史页 rebase 开关：勾选后 pull 走 --rebase，本地提交重放到远程之上。
func TestPullActiveRepoRebase(t *testing.T) {
	origin, work := cloneWithRemote(t)
	if err := os.WriteFile(filepath.Join(work, "local.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "add", ".")
	gitRun(t, work, "commit", "-m", "local work")
	pushCommit(t, origin, "remote.txt", "remote\n", "remote work")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = work
	repos := git.FindRepos(work)
	ws.applyScanState(work, repos, git.CollectChanges(repos))
	ws.mainTab = 1
	ws.loadCommits()

	ws.pullActiveRepo(true)
	if ws.errText() != "" {
		t.Fatalf("pull err = %q, want success", ws.errText())
	}
	if got, want := strings.TrimSpace(gitRun(t, work, "rev-parse", "HEAD^")),
		strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD")); got != want {
		t.Errorf("HEAD^ = %s, want local commit rebased onto %s", got, want)
	}
	for _, name := range []string{"local.txt", "remote.txt"} {
		if _, err := os.Stat(filepath.Join(work, name)); err != nil {
			t.Errorf("%s missing after rebase pull: %v", name, err)
		}
	}
}

// 历史列表世代：重载后旧分页结果不得追加；同代结果即使切走 tab 也解除
// 在途标记，避免回到历史页后卡在加载中。
func TestCommitLoadGeneration(t *testing.T) {
	repo := "/r/a"
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.mainTab = 1
	ws.activeRepo = repo
	ws.hist.gen = 2
	ws.hist.all = []git.CommitRow{{Hash: "fresh"}}

	ws.applyCommitPage(1, repo, []git.CommitRow{{Hash: "stale"}}, nil)
	ws.applyCommits(1, repo, []git.CommitRow{{Hash: "stale"}}, nil)
	if len(ws.hist.all) != 1 || ws.hist.all[0].Hash != "fresh" {
		t.Errorf("all = %+v, stale generation must be discarded", ws.hist.all)
	}

	// 同代结果在切走 tab 后到达：数据不落地，但在途标记要解除
	ws.hist.gen = 3
	ws.hist.loading = true
	ws.hist.fetching = true
	ws.mainTab = 0
	ws.applyCommits(3, repo, nil, nil)
	ws.applyCommitPage(3, repo, []git.CommitRow{{Hash: "late"}}, nil)
	if ws.hist.loading || ws.hist.fetching {
		t.Errorf("loading/fetching = %v/%v, want both cleared", ws.hist.loading, ws.hist.fetching)
	}
	if ws.hist.all[0].Hash != "fresh" {
		t.Errorf("all = %+v, off-tab result must not apply", ws.hist.all)
	}
}

// 历史页远程操作控件与顶栏「全部 fetch」入口在场。
func TestHistoryRemoteControls(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.repos = []git.Repo{{Path: "D:/repos/a", Name: "a", Rel: "a"}}
	ws.activeRepo = "D:/repos/a"
	ws.hist.statusByRepo = map[string]git.BranchStatus{
		"D:/repos/a": {Branch: "main", Upstream: "origin/main", Ahead: 2, Behind: 3},
	}
	ws.rebuildDerived()
	ws.hist.loading = true // 阻止渲染帧触发真实 git 读取

	tt := ui.NewTester(ws.view, 1180, 800)
	for _, label := range []string{"fetch", "pull", "pull 变体", "隐藏 merge", "全部 fetch", "刷新", "main", "↑2 ↓3"} {
		if !tt.HasText(label) {
			t.Errorf("texts = %q, want %q", tt.Texts(), label)
		}
	}
	// 输入框只放仓库名；分支是独立的灰色文本
	comboValue := ""
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleComboBox {
			comboValue = info.Value
			break
		}
	}
	if comboValue != "a" {
		t.Errorf("combo value = %q, want repo name only", comboValue)
	}
	if _, ok := tt.Access("搜索提交"); !ok {
		t.Errorf("search field missing: %v", tt.AccessAll())
	}
}

// 提交过滤：message / 作者 / 完整与短 hash，忽略大小写；与隐藏 merge 叠加。
func TestCommitFilter(t *testing.T) {
	ws := newWorkspace(nil)
	ws.hist.all = []git.CommitRow{
		{Hash: "abcdef123456", ShortHash: "abcdef1", Author: "Alice", Subject: "fix parser"},
		{Hash: "123456abcdef", ShortHash: "123456a", Author: "Bob", Subject: "docs update"},
		{Hash: "fedcba654321", ShortHash: "fedcba6", Author: "alice", Subject: "Fix tests", Merge: true},
	}
	cases := []struct {
		query      string
		hideMerges bool
		want       int
	}{
		{"fix", false, 2},
		{"ALICE", false, 2},
		{"abcdef1", false, 1},
		{"abcdef123456", false, 1},
		{"nope", false, 0},
		{"fix", true, 1},
		{"", false, 3},
	}
	for _, tc := range cases {
		ws.hist.query = tc.query
		ws.hist.hideMerges = tc.hideMerges
		ws.refilterCommits()
		if len(ws.hist.commits) != tc.want {
			t.Errorf("query=%q hideMerges=%v: commits=%d, want %d",
				tc.query, tc.hideMerges, len(ws.hist.commits), tc.want)
		}
		if ws.hist.sel != -1 {
			t.Errorf("query=%q: sel=%d, want reset to -1", tc.query, ws.hist.sel)
		}
	}
}

// 搜索首次输入触发全量历史加载：替换分页数据并在本地过滤。
func TestLoadAllCommitsForSearch(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	for _, msg := range []string{"second commit", "third commit"} {
		gitRun(t, root, "commit", "--allow-empty", "-m", msg)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.activeRepo = root
	ws.mainTab = 1
	// 模拟只加载了第一页
	ws.hist.all = []git.CommitRow{{Hash: "stale", Subject: "init"}}
	ws.hist.query = "third"

	ws.loadAllCommits()
	if !ws.hist.allLoaded || !ws.hist.done || ws.hist.allLoading {
		t.Errorf("allLoaded=%v done=%v allLoading=%v, want loaded and done",
			ws.hist.allLoaded, ws.hist.done, ws.hist.allLoading)
	}
	if len(ws.hist.all) != 3 {
		t.Errorf("all = %d commits, want full history 3", len(ws.hist.all))
	}
	if len(ws.hist.commits) != 1 || ws.hist.commits[0].Subject != "third commit" {
		t.Errorf("commits = %+v, want only third commit", ws.hist.commits)
	}
	if st := ws.hist.statusByRepo[root]; st.Branch != "main" {
		t.Errorf("branch status = %+v, want main loaded with the full history", st)
	}
}

// 全量加载失败：保留已加载页、停止自动重试并反馈错误。
func TestLoadAllCommitsFailure(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/missing"
	ws.activeRepo = "D:/missing"
	ws.mainTab = 1
	ws.hist.query = "fix"
	ws.hist.all = []git.CommitRow{{Hash: "kept", Subject: "kept"}}

	ws.loadAllCommits()
	if !ws.hist.allTried || ws.hist.allLoading {
		t.Errorf("allTried=%v allLoading=%v, want tried and not loading", ws.hist.allTried, ws.hist.allLoading)
	}
	if !strings.Contains(ws.errText(), "搜索提交失败") {
		t.Errorf("errText = %q, want search failure", ws.errText())
	}
	if len(ws.hist.all) != 1 || ws.hist.all[0].Hash != "kept" {
		t.Errorf("all = %+v, want previous page kept", ws.hist.all)
	}
}

// 搜索框输入后列表按 message 过滤并显示匹配数。
func TestHistorySearchFieldFilters(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.activeRepo = "D:/repos/a"
	ws.hist.all = []git.CommitRow{
		{Hash: "h1", ShortHash: "h1", Author: "Alice", Date: "2026-11-16", Subject: "fix parser"},
		{Hash: "h2", ShortHash: "h2", Author: "Bob", Date: "2026-11-15", Subject: "docs update"},
	}
	ws.hist.commits = ws.hist.all
	ws.hist.allLoaded = true // 阻止搜索触发真实的全量加载
	ws.hist.done = true

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	info, ok := tt.Access("搜索提交")
	if !ok {
		t.Fatalf("search field missing: %v", tt.AccessAll())
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
	tt.Type("fix")
	tt.Frame()

	if ws.hist.query != "fix" {
		t.Fatalf("query = %q, want fix", ws.hist.query)
	}
	if len(ws.hist.commits) != 1 || ws.hist.commits[0].Subject != "fix parser" {
		t.Errorf("commits = %+v, want only fix parser", ws.hist.commits)
	}
	if !tt.HasText("1 条") {
		t.Errorf("texts = %q, want match count", tt.Texts())
	}
	if tt.HasText("docs update") {
		t.Error("non-matching commit still visible")
	}
}

// 分支状态拆分：分支名与跟踪标记分开渲染；长分支名截断不影响计数。
func TestBranchStatusParts(t *testing.T) {
	cases := []struct {
		st            git.BranchStatus
		branch, track string
	}{
		{git.BranchStatus{}, "", ""},
		{git.BranchStatus{Branch: "main"}, "main", ""},
		{git.BranchStatus{Branch: "main", Upstream: "origin/main"}, "main", ""},
		{git.BranchStatus{Branch: "main", Upstream: "origin/main", Ahead: 2}, "main", "↑2"},
		{git.BranchStatus{Branch: "main", Upstream: "origin/main", Behind: 3}, "main", "↓3"},
		{git.BranchStatus{Branch: "main", Upstream: "origin/main", Ahead: 2, Behind: 3}, "main", "↑2 ↓3"},
		{git.BranchStatus{Branch: "main", Upstream: "origin/main", Gone: true, Ahead: 2}, "main", "上游已删除"},
	}
	for _, tc := range cases {
		branch, track := branchStatusParts(tc.st)
		if branch != tc.branch || track != tc.track {
			t.Errorf("branchStatusParts(%+v) = %q, %q, want %q, %q",
				tc.st, branch, track, tc.branch, tc.track)
		}
	}
}

// 下拉项第二行：分支名 + ahead/behind 合并为一行。
func TestRepoBranchLine(t *testing.T) {
	cases := []struct {
		st   git.BranchStatus
		want string
	}{
		{git.BranchStatus{}, ""},
		{git.BranchStatus{Branch: "main"}, "main"},
		{git.BranchStatus{Branch: "main", Upstream: "origin/main", Ahead: 1, Behind: 2}, "main ↑1 ↓2"},
		{git.BranchStatus{Branch: "main", Upstream: "origin/main", Gone: true}, "main 上游已删除"},
	}
	for _, tc := range cases {
		if got := repoBranchLine(tc.st); got != tc.want {
			t.Errorf("repoBranchLine(%+v) = %q, want %q", tc.st, got, tc.want)
		}
	}
}

// 仓库下拉过滤：忽略大小写，前缀匹配排前，空查询返回全部。
func TestMatchRepoLabels(t *testing.T) {
	opts := []string{"alpha", "beta", "alphabet"}
	cases := []struct {
		query string
		want  []string
	}{
		{"", opts},
		{"alp", []string{"alpha", "alphabet"}},
		{"BET", []string{"beta", "alphabet"}},
		{"pha", []string{"alpha", "alphabet"}},
		{"none", nil},
	}
	for _, tc := range cases {
		got := matchRepoLabels(opts, tc.query)
		if len(got) != len(tc.want) {
			t.Errorf("matchRepoLabels(%q) = %v, want %v", tc.query, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("matchRepoLabels(%q) = %v, want %v", tc.query, got, tc.want)
				break
			}
		}
	}
}

// 长展示串在输入框内裁剪：文本区域止于箭头左侧，不会画到箭头下面。
func TestRepoComboTextStopsAtArrow(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.repos = []git.Repo{{
		Path: "D:/repos/a-very-long-repository-name-for-testing",
		Name: "a-very-long-repository-name-for-testing",
		Rel:  "a-very-long-repository-name-for-testing",
	}}
	ws.activeRepo = ws.repos[0].Path
	ws.rebuildDerived()
	ws.hist.statusByRepo = map[string]git.BranchStatus{
		ws.activeRepo: {
			Branch:   "visit_checkin_approval_for_some_really_long_feature_branch",
			Upstream: "origin/visit_checkin_approval_for_some_really_long_feature_branch",
			Ahead:    9,
		},
	}
	ws.hist.loading = true

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	var combo, arrow ui.Rect
	comboOK, arrowOK := false, false
	for _, info := range tt.AccessAll() {
		switch {
		case info.Role == ui.RoleComboBox:
			combo, comboOK = info.Bounds, true
		case info.Name == "打开仓库列表":
			arrow, arrowOK = info.Bounds, true
		}
	}
	if !comboOK || !arrowOK {
		t.Fatalf("combo=%v arrow=%v, want both", comboOK, arrowOK)
	}
	if combo.X+combo.W > arrow.X {
		t.Errorf("combo text box %+v overlaps arrow %+v, want text to stop before the arrow", combo, arrow)
	}
}

// 仓库下拉：打开时每项显示仓库名 + 分支/ahead-behind 与当前标记；
// 键入过滤后可选仓库，选中值回填输入框。
func TestRepoDropdownShowsBranchAndFilters(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.repos = []git.Repo{
		{Path: "D:/repos/demo-repo", Name: "demo-repo", Rel: "demo-repo"},
		{Path: "D:/repos/other-repo", Name: "other-repo", Rel: "other-repo"},
		{Path: "D:/repos/third-repo", Name: "third-repo", Rel: "third-repo"},
	}
	ws.activeRepo = "D:/repos/demo-repo"
	ws.rebuildDerived()
	ws.hist.statusByRepo = map[string]git.BranchStatus{
		"D:/repos/demo-repo": {Branch: "transfer_data_app_refresh", Upstream: "origin/transfer_data_app_refresh", Ahead: 1, Behind: 2},
		"D:/repos/other-repo":        {Branch: "main"},
		"D:/repos/third-repo":        {Branch: "feature/login", Upstream: "origin/feature/login", Ahead: 3},
	}
	ws.hist.loading = true // 阻止历史加载

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.Click("打开仓库列表"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"transfer_data_app_refresh ↑1 ↓2", "main", "feature/login ↑3", "当前"} {
		if !tt.HasText(want) {
			t.Errorf("texts = %q, want %q", tt.Texts(), want)
		}
	}

	// 键入过滤：只剩 third-repo
	tt.Type("third")
	tt.Frame()
	if !tt.HasText("third-repo") {
		t.Errorf("texts = %q, want filtered option", tt.Texts())
	}
	if tt.HasText("other-repo") || tt.HasText("demo-repo") {
		t.Errorf("texts = %q, want other options filtered out", tt.Texts())
	}

	// 选择 third-repo：选中值回填输入框
	if err := tt.Click("third-repo"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ws.activeRepo != "D:/repos/third-repo" {
		t.Errorf("activeRepo = %q, want third-repo", ws.activeRepo)
	}
	if ws.hist.repoCombo != "third-repo" || ws.hist.repoText != "third-repo" {
		t.Errorf("repoCombo/repoText = %q/%q, want third-repo", ws.hist.repoCombo, ws.hist.repoText)
	}
	if ws.hist.repoOpen {
		t.Error("repoOpen must be false after choosing")
	}
}

// 复制 hash：详情头部按钮写入完整哈希。
func TestCopyCommitHash(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.activeRepo = "D:/repos/a"
	ws.hist.all = []git.CommitRow{{Hash: "abcdef123456", ShortHash: "abcdef1", Author: "a", Date: "d", Subject: "s"}}
	ws.hist.commits = ws.hist.all
	ws.hist.allLoaded = true
	ws.hist.done = true
	ws.hist.sel = 0

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.Click("复制 hash"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != "abcdef123456" {
		t.Errorf("clipboard = %q, want full hash", got)
	}
}

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

// watch 合并落地比对：受影响仓库的改动段与当前一致（hint 命中但内容没变）
// 时不重建派生，真变了才合并——实抓：合并走了整体替换重建。
func TestMergeChangesSkipsUnchanged(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	p1 := ws.changeByAbs[root+"/base.txt"]

	// 原样重存（编辑器保存未改内容）：重读段一致 → 不重建。纯内容修改
	// 不算变化——比对对象是改动列表（git 状态），内容级修改产生的列表
	// 逐值相同，正确地被跳过
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.handleFsEvents(root, []string{"base.txt"})
	if ws.changeByAbs[root+"/base.txt"] != p1 {
		t.Fatal("derived index rebuilt on unchanged merge")
	}

	// 改动列表真变了（未暂存 → 暂存）：合并并重建
	gitCmd(t, root, "add", "base.txt")
	ws.handleFsEvents(root, []string{"base.txt"})
	if ws.changeByAbs[root+"/base.txt"] == p1 {
		t.Fatal("derived index not rebuilt on changed merge")
	}
}
