package git

import "strings"

// StatusFile 是 porcelain v1 的一行：X 是 staged 位，Y 是 unstaged 位。
type StatusFile struct {
	X, Y byte
	// Path 一律是文件现所在的新路径；改名/复制行（R/C，形如 "old -> new"）
	// 的旧路径单独记在 OrigPath。打开、分组、暂存等落盘操作都按新路径走。
	Path      string
	OrigPath  string
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
// OrigPath 是改名/复制条目的旧路径（diff 旧侧、展示用），非改名为空。
type Change struct {
	RepoPath string
	RepoRel  string
	Path     string
	OrigPath string
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
		// 改名/复制一行描述两个路径：Path 取新路径（文件现所在），旧路径
		// 单独携带。porcelain v1 无字段分隔符，文件名真含 " -> " 时本格式
		// 有固有歧义，按首个分隔拆分（该场景极罕见，v2 的 TAB 分隔才能根治）
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			if i := strings.Index(f.Path, " -> "); i >= 0 {
				f.OrigPath = f.Path[:i]
				f.Path = f.Path[i+4:]
			}
		}
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
			result = append(result, Change{e.repo.Path, e.repo.Rel, f.Path, f.OrigPath, "unstaged", string(f.Y)})
		}
		for _, f := range e.status.Staged {
			result = append(result, Change{e.repo.Path, e.repo.Rel, f.Path, f.OrigPath, "staged", string(f.X)})
		}
		for _, f := range e.status.Untracked {
			result = append(result, Change{e.repo.Path, e.repo.Rel, f.Path, f.OrigPath, "untracked", "?"})
		}
	}
	return result
}
