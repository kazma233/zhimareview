package git

import (
	"os"
	"strings"
	"testing"
)

// clonePair 建本地 bare 远程与两个 clone：origin 用于推送新提交，work 是被测仓库。
func clonePair(t *testing.T) (origin, work string) {
	t.Helper()
	base := filepathToSlash(t.TempDir())
	remote := base + "/remote.git"
	seed := base + "/seed"
	gitIn(t, base, "init", "--bare", "-b", "main", remote)
	gitIn(t, base, "init", "-b", "main", seed)
	gitIn(t, seed, "config", "user.name", "t")
	gitIn(t, seed, "config", "user.email", "t@t")
	write(t, seed+"/base.txt", "base\n")
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-m", "base")
	gitIn(t, seed, "remote", "add", "origin", remote)
	gitIn(t, seed, "push", "-u", "origin", "main")

	origin = base + "/origin"
	work = base + "/work"
	// clone 时单次 -c 关 autocrlf：clone 自带的 checkout 也走 LF，不依赖
	// 机器的系统级配置（事后设 config 救不回 clone 已经写出的 CRLF 工作区）
	gitIn(t, base, "-c", "core.autocrlf=false", "clone", remote, origin)
	gitIn(t, base, "-c", "core.autocrlf=false", "clone", remote, work)
	for _, dir := range []string{origin, work} {
		gitIn(t, dir, "config", "user.name", "t")
		gitIn(t, dir, "config", "user.email", "t@t")
		// autocrlf 关掉：clone 的 checkout 不依赖机器的系统级配置换行行为
		gitIn(t, dir, "config", "core.autocrlf", "false")
	}
	return origin, work
}

// commitFile 在仓库里写文件并提交。
func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	write(t, dir+"/"+name, content)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", message)
}

func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, dir, "rev-parse", rev))
}

// fetch 只更新远程引用，不动当前分支；历史页的 --all 数据源能看到新提交。
func TestFetchRepoUpdatesRemoteRefs(t *testing.T) {
	origin, work := clonePair(t)
	commitFile(t, origin, "remote.txt", "remote\n", "remote work")
	gitIn(t, origin, "push")

	before := revParse(t, work, "HEAD")
	if err := FetchRepo(work); err != nil {
		t.Fatal(err)
	}
	if got, want := revParse(t, work, "origin/main"), revParse(t, origin, "HEAD"); got != want {
		t.Errorf("origin/main = %s, want %s", got, want)
	}
	if got := revParse(t, work, "HEAD"); got != before {
		t.Errorf("HEAD = %s, fetch must not move the checked-out branch", got)
	}
	commits, err := LogCommits(work, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range commits {
		if r.Subject == "remote work" {
			found = true
		}
	}
	if len(commits) != 2 || !found {
		t.Errorf("commits = %+v, want fetched commit visible via --all", commits)
	}
}

// 默认 pull 只允许快进：远程前进时工作区与 HEAD 一起前进。
func TestPullRepoFastForward(t *testing.T) {
	origin, work := clonePair(t)
	commitFile(t, origin, "remote.txt", "remote\n", "remote work")
	gitIn(t, origin, "push")

	if err := PullRepo(work, false); err != nil {
		t.Fatal(err)
	}
	if got, want := revParse(t, work, "HEAD"), revParse(t, origin, "HEAD"); got != want {
		t.Errorf("HEAD = %s, want fast-forwarded to %s", got, want)
	}
	data, err := os.ReadFile(work + "/remote.txt")
	if err != nil || string(data) != "remote\n" {
		t.Errorf("remote.txt = %q, %v, want pulled content", data, err)
	}
}

// 历史分叉时默认 pull 失败且不动本地 HEAD（不自动 merge、不留下冲突现场）。
func TestPullRepoDivergedFailsWithoutMerge(t *testing.T) {
	origin, work := clonePair(t)
	commitFile(t, work, "local.txt", "local\n", "local work")
	commitFile(t, origin, "remote.txt", "remote\n", "remote work")
	gitIn(t, origin, "push")

	before := revParse(t, work, "HEAD")
	err := PullRepo(work, false)
	if err == nil {
		t.Fatal("diverged pull must fail with --ff-only")
	}
	if !strings.Contains(err.Error(), "fast-forward") {
		t.Errorf("err = %v, want a fast-forward refusal", err)
	}
	if got := revParse(t, work, "HEAD"); got != before {
		t.Errorf("HEAD = %s, want unchanged %s", got, before)
	}
	if _, found, _ := ShowObject(work, "HEAD", "remote.txt"); found {
		t.Error("remote.txt must not appear without merge")
	}
}

// rebase 选项：本地提交重放到远程之上，双方内容都在。
func TestPullRepoRebase(t *testing.T) {
	origin, work := clonePair(t)
	commitFile(t, work, "local.txt", "local\n", "local work")
	commitFile(t, origin, "remote.txt", "remote\n", "remote work")
	gitIn(t, origin, "push")

	if err := PullRepo(work, true); err != nil {
		t.Fatal(err)
	}
	remoteHead := revParse(t, origin, "HEAD")
	if got := revParse(t, work, "HEAD^"); got != remoteHead {
		t.Errorf("HEAD^ = %s, want rebased local commit on %s", got, remoteHead)
	}
	if got := strings.TrimSpace(gitIn(t, work, "log", "--format=%s")); !strings.Contains(got, "local work") ||
		!strings.Contains(got, "remote work") {
		t.Errorf("log = %q, want both local and remote commits", got)
	}
	for _, name := range []string{"local.txt", "remote.txt"} {
		if _, err := os.Stat(work + "/" + name); err != nil {
			t.Errorf("%s missing after rebase pull: %v", name, err)
		}
	}
}

// FetchRepos：结果按输入顺序；无 remote 的仓库不报错，坏 remote 返回错误。
func TestFetchRepos(t *testing.T) {
	_, work := clonePair(t)
	plain := filepathToSlash(t.TempDir()) + "/plain"
	initRepo(t, plain)
	bad := filepathToSlash(t.TempDir()) + "/bad"
	initRepo(t, bad)
	gitIn(t, bad, "remote", "add", "origin", bad+"/missing.git")

	repos := []Repo{
		{Path: work, Name: "work", Rel: "work"},
		{Path: plain, Name: "plain", Rel: "plain"},
		{Path: bad, Name: "bad", Rel: "bad"},
	}
	results := FetchRepos(repos)
	if len(results) != len(repos) {
		t.Fatalf("results = %d, want %d", len(results), len(repos))
	}
	for i, want := range repos {
		if results[i].Repo.Path != want.Path {
			t.Errorf("results[%d].Repo = %+v, want %+v", i, results[i].Repo, want)
		}
	}
	if results[0].Err != nil {
		t.Errorf("work fetch err = %v, want nil", results[0].Err)
	}
	if results[1].Err != nil {
		t.Errorf("plain fetch err = %v, want nil (no remote is not an error)", results[1].Err)
	}
	if results[2].Err == nil {
		t.Error("bad remote fetch must fail")
	}
}
