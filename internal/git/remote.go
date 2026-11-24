package git

import "sync"

// FetchRepo 拉取仓库的全部远程并清理已删除的远程分支。
// 无 remote 的仓库 git 退出码为 0，与「已是最新」同样处理。
func FetchRepo(repoPath string) error {
	_, err := Run(repoPath, "fetch", "--all", "--prune")
	return err
}

// PullRepo 拉取当前分支：
// rebase=false 只允许快进（历史分叉即失败，不自动 merge 或产生冲突）；
// rebase=true 把本地提交变基到上游之上，冲突时 git 非零退出并留下变基现场。
func PullRepo(repoPath string, rebase bool) error {
	args := []string{"pull", "--ff-only"}
	if rebase {
		args = []string{"pull", "--rebase"}
	}
	_, err := runWriteGit(repoPath, args...)
	return err
}

// FetchResult 是逐仓库 fetch 的结果（Repo 供调用方标注失败仓库）。
type FetchResult struct {
	Repo Repo
	Err  error
}

// FetchRepos 并发拉取多个仓库的远程引用；结果保持输入顺序。
func FetchRepos(repos []Repo) []FetchResult {
	return FetchReposProgress(repos, nil, nil)
}

// FetchReposProgress 并发拉取多个仓库；每个仓库开始与结束时回调，供调用方
// 展示进度。回调在后台 goroutine 上串行执行，切回界面线程由调用方负责；
// 结果保持输入顺序。
func FetchReposProgress(repos []Repo, onStart func(Repo), onDone func(FetchResult)) []FetchResult {
	var mu sync.Mutex
	report := func(fn func()) {
		if fn == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fn()
	}
	return mapLimit(repos, 8, func(repo Repo) FetchResult {
		report(func() {
			if onStart != nil {
				onStart(repo)
			}
		})
		res := FetchResult{Repo: repo, Err: FetchRepo(repo.Path)}
		report(func() {
			if onDone != nil {
				onDone(res)
			}
		})
		return res
	})
}
