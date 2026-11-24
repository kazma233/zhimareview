package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"zhimareview/internal/git"
)

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
		"D:/repos/demo-repo":  {Branch: "transfer_data_app_refresh", Upstream: "origin/transfer_data_app_refresh", Ahead: 1, Behind: 2},
		"D:/repos/other-repo": {Branch: "main"},
		"D:/repos/third-repo": {Branch: "feature/login", Upstream: "origin/feature/login", Ahead: 3},
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
