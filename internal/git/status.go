package git

import "strings"

// StatusFile 是 porcelain v1 的一行：X 是 staged 位，Y 是 unstaged 位。
type StatusFile struct {
	X, Y      byte
	Path      string
	Untracked bool
}

// RepoStatus 是一次 git status 的三区分类结果。
type RepoStatus struct {
	Branch    string
	Staged    []StatusFile
	Unstaged  []StatusFile
	Untracked []StatusFile
}

// Change 是压平到侧栏的一个改动文件。
// Section 取值 unstaged / staged / untracked；Code 是对应的 porcelain 状态位。
type Change struct {
	RepoPath string
	RepoRel  string
	Path     string
	Section  string
	Code     string
}

// ReadStatus 读取一个仓库的分支名与三区改动。
// -uall 让未跟踪目录展开成逐个文件，避免出现 "dir/" 这种无法打开的条目。
func ReadStatus(repoPath string) (*RepoStatus, error) {
	out, err := Run(repoPath, "status", "--porcelain=v1", "--branch", "-uall")
	if err != nil {
		return nil, err
	}
	st := &RepoStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			rest := line[3:]
			// "main...origin/main [ahead 1]" 只取本地段；detached 是 "HEAD (no branch)"
			if i := strings.Index(rest, "..."); i >= 0 {
				rest = rest[:i]
			}
			if i := strings.IndexByte(rest, ' '); i >= 0 {
				rest = rest[:i]
			}
			st.Branch = rest
			continue
		}
		if len(line) < 4 {
			continue
		}
		x, y := line[0], line[1]
		f := StatusFile{X: x, Y: y, Path: line[3:], Untracked: x == '?' && y == '?'}
		switch {
		case f.Untracked:
			st.Untracked = append(st.Untracked, f)
		default:
			if x != ' ' && x != '?' {
				st.Staged = append(st.Staged, f)
			}
			if y != ' ' && y != '?' {
				st.Unstaged = append(st.Unstaged, f)
			}
		}
	}
	return st, nil
}

// CollectChanges 逐仓库读取状态并压平成侧栏改动列表。
// 并发上限 8 个 git 进程；单个仓库失败不影响其它仓库。
// 侧栏分区分组顺序：先 unstaged、再 staged、最后 untracked。
func CollectChanges(repos []Repo) []Change {
	type entry struct {
		repo   Repo
		status *RepoStatus
	}
	perRepo := mapLimit(repos, 8, func(repo Repo) *entry {
		st, err := ReadStatus(repo.Path)
		if err != nil {
			return nil
		}
		return &entry{repo: repo, status: st}
	})

	var result []Change
	for _, e := range perRepo {
		if e == nil {
			continue
		}
		for _, f := range e.status.Unstaged {
			result = append(result, Change{e.repo.Path, e.repo.Rel, f.Path, "unstaged", string(f.Y)})
		}
		for _, f := range e.status.Staged {
			result = append(result, Change{e.repo.Path, e.repo.Rel, f.Path, "staged", string(f.X)})
		}
		for _, f := range e.status.Untracked {
			result = append(result, Change{e.repo.Path, e.repo.Rel, f.Path, "untracked", "?"})
		}
	}
	return result
}
