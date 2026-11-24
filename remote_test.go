package main

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// fetchAllFixture 建三个仓库：正常、无 remote（不报错）、坏 remote。
func fetchAllFixture(t *testing.T) ([]git.Repo, string) {
	t.Helper()
	_, work := cloneWithRemote(t)
	plain := filepath.ToSlash(t.TempDir()) + "/plain"
	gitInit(t, plain)
	bad := filepath.ToSlash(t.TempDir()) + "/bad"
	gitInit(t, bad)
	gitRun(t, bad, "remote", "add", "origin", bad+"/missing.git")
	return []git.Repo{
		{Path: work, Name: "work", Rel: "work"},
		{Path: plain, Name: "plain", Rel: "plain"},
		{Path: bad, Name: "bad", Rel: "bad"},
	}, work
}

// 有主线程队列时按仓库流式回推进度：每个仓库 start/done 各投递一次，
// 最终再以 FetchRepos 的结果校准整张进度表。
func TestFetchAllStreamsProgress(t *testing.T) {
	repos, work := fetchAllFixture(t)
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = work
	ws.repos = repos
	ws.activeRepo = work

	q := &testQueue{}
	var posts atomic.Int32
	ws.post = func(f func()) {
		posts.Add(1)
		q.post(f)
	}

	ws.fetchAllRepos()
	waitFor(t, func() bool {
		q.drain()
		return !ws.fetchAll.running
	}, 5*time.Second, "fetch progress not applied")
	q.drain()

	if ws.fetchAll.done != len(repos) || ws.fetchAll.failed != 1 {
		t.Fatalf("progress = %d done / %d failed, want %d/1",
			ws.fetchAll.done, ws.fetchAll.failed, len(repos))
	}
	// 每个仓库 start+done 各一次，外加最终结果投递（reloadCommits 还会投递，
	// 只作下限断言）
	if got, want := posts.Load(), int32(2*len(repos)+1); got < want {
		t.Errorf("posts = %d, want at least %d (streaming progress not wired)", got, want)
	}
}

// 运行中再次点「全部 fetch」只重开模态，不重启 fetch、不重置进度。
func TestFetchAllReopensProgressWhileRunning(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "/root"
	repos := []git.Repo{{Path: "/root/a", Name: "a", Rel: "a"}}
	ws.repos = repos
	ws.fetchAll.start(repos)
	ws.fetchAll.open = false
	ws.fetchAll.done = 1
	ws.fetchAll.items[0].state = fetchOK

	ws.fetchAllRepos()

	if !ws.fetchAll.open || !ws.fetchAll.running {
		t.Fatalf("reopen = open %v running %v, want both true", ws.fetchAll.open, ws.fetchAll.running)
	}
	if ws.fetchAll.done != 1 || len(ws.fetchAll.items) != 1 || ws.fetchAll.items[0].state != fetchOK {
		t.Fatalf("progress reset on reopen: %+v", ws.fetchAll)
	}
}

// 全部 fetch：进度落到模态，坏 remote 的错误留在模态里，不另开错误弹窗。
func TestFetchAllReposAggregatesErrors(t *testing.T) {
	repos, work := fetchAllFixture(t)
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = filepath.ToSlash(t.TempDir())
	ws.repos = repos
	ws.activeRepo = work
	ws.mainTab = 1
	ws.loadCommits()

	ws.fetchAllRepos()
	if ws.fetchAll.running {
		t.Error("fetchAll.running must be cleared after completion")
	}
	if !ws.fetchAll.open {
		t.Error("progress modal must stay open with the result")
	}
	if ws.fetchAll.done != 3 || ws.fetchAll.failed != 1 {
		t.Errorf("progress = %d done / %d failed, want 3/1", ws.fetchAll.done, ws.fetchAll.failed)
	}
	if ws.errOpen {
		t.Error("fetch failures must stay in the progress modal, not open the error dialog")
	}
	if !ws.errSeen {
		t.Error("failures shown in the progress modal must count as read (no red dot)")
	}
	if !strings.Contains(ws.errText(), "fetch 失败 1/3") || !strings.Contains(ws.errText(), "bad") {
		t.Errorf("errText = %q, want one named failure out of three", ws.errText())
	}
	if len(ws.hist.commits) == 0 {
		t.Error("commits must be reloaded after fetch all")
	}
	// 完整 git 错误留在进度项里，供模态展开
	if it := ws.fetchAll.items[2]; it.state != fetchFailed || !strings.Contains(it.err, "missing.git") {
		t.Errorf("bad item = %+v, want failed with git error", it)
	}

	// 不在历史页时全部 fetch 也要让历史失效，进入页面时重新加载
	ws.mainTab = 0
	ws.hist.all = []git.CommitRow{{Hash: "stale"}}
	ws.fetchAllRepos()
	if ws.hist.all != nil {
		t.Error("fetch all off the history tab must invalidate commits")
	}
}

// 进度状态机：开始/完成/重复回调/最终校准都只计一次。
func TestFetchAllPaneTracksProgress(t *testing.T) {
	repos := []git.Repo{{Path: "/a", Name: "a"}, {Path: "/b", Name: "b"}}
	var f fetchAllPane
	f.start(repos)
	if !f.open || !f.running || f.done != 0 || len(f.items) != 2 {
		t.Fatalf("start = %+v, want open/running with two waiting items", f)
	}
	f.markStart("/a")
	if f.items[0].state != fetchRunning {
		t.Fatalf("state after start = %v, want running", f.items[0].state)
	}
	f.markDone(git.FetchResult{Repo: repos[0]})
	if f.items[0].state != fetchOK || f.done != 1 || f.failed != 0 {
		t.Fatalf("after done = %+v, want 1 ok / 0 failed", f)
	}
	f.markDone(git.FetchResult{Repo: repos[0]}) // 迟到/重复回调
	if f.done != 1 {
		t.Fatalf("done = %d after duplicate callback, want 1", f.done)
	}
	f.applyResults([]git.FetchResult{
		{Repo: repos[0]},
		{Repo: repos[1], Err: errors.New("boom")},
	})
	if f.running || f.done != 2 || f.failed != 1 {
		t.Fatalf("applyResults = %+v, want finished with 2 done / 1 failed", f)
	}
	if f.items[1].state != fetchFailed || f.items[1].err != "boom" {
		t.Fatalf("failed item = %+v, want boom", f.items[1])
	}
	if got := f.errorText(); !strings.Contains(got, "b — boom") {
		t.Fatalf("errorText = %q, want repo + full error", got)
	}
}

// 运行中收起模态后失败：错误回落到错误弹窗；成功则清掉旧错误。
func TestFetchAllFailureFallsBackToDialogWhenHidden(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "/root"
	repos := []git.Repo{{Path: "/root/a", Name: "a", Rel: "a"}}
	ws.repos = repos

	ws.fetchAll.start(repos)
	ws.fetchAll.open = false // 用户运行中收起模态
	ws.applyFetchAll("/root", []git.FetchResult{{Repo: repos[0], Err: errors.New("boom")}})
	if !ws.errOpen {
		t.Fatal("hidden modal + failure must open the error dialog")
	}
	if !strings.Contains(ws.errText(), "boom") {
		t.Fatalf("errText = %q, want fetch failure", ws.errText())
	}

	ws.fetchAll.start(repos)
	ws.fetchAll.open = false
	ws.applyFetchAll("/root", []git.FetchResult{{Repo: repos[0]}})
	if ws.errOpen || ws.err != nil {
		t.Fatalf("hidden modal + success = err %+v open %v, want cleared", ws.err, ws.errOpen)
	}
}

// 模态展示逐仓库状态与完整错误，复制错误与关闭都在这一个模态里完成。
func TestFetchAllModalShowsProgressAndErrors(t *testing.T) {
	repos, work := fetchAllFixture(t)
	a := workApp(work)
	ws := a.activeTab()
	ws.repos = repos
	ws.activeRepo = work
	ws.fetchAllRepos()

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	tt.Frame()

	if !tt.HasText("全部 fetch") || !tt.HasText("拉取完成：3 个仓库，1 个失败") {
		t.Fatalf("modal title/subtitle missing: %q", tt.Texts())
	}
	if !tt.HasText("已处理 3/3") {
		t.Fatalf("progress summary missing: %q", tt.Texts())
	}
	if !tt.HasText("bad") || !tt.HasText("失败") {
		t.Fatalf("repo state missing: %q", tt.Texts())
	}
	if joined := strings.Join(tt.Texts(), "\n"); !strings.Contains(joined, "missing.git") {
		t.Fatalf("full error text missing: %q", joined)
	}
	if ws.errOpen {
		t.Fatal("fetch error must not open a second dialog over the modal")
	}

	if err := tt.Click("复制错误"); err != nil {
		t.Fatalf("click copy: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if got := tt.Clipboard(); !strings.Contains(got, "bad — ") || !strings.Contains(got, "missing.git") {
		t.Fatalf("clipboard = %q, want repo and full error", got)
	}

	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("click close: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if ws.fetchAll.open {
		t.Fatal("modal still open after close")
	}
	if ws.err == nil || ws.errOpen || !ws.errSeen {
		t.Fatalf("after close err=%+v open=%v seen=%v, want retained/read/closed",
			ws.err, ws.errOpen, ws.errSeen)
	}
}
