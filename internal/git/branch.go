package git

import (
	"strconv"
	"strings"
)

// BranchStatus 是当前分支与上游的跟踪状态。
// Detached HEAD 没有当前分支，返回零值；Gone 表示上游引用已被删除。
type BranchStatus struct {
	Branch   string
	Upstream string
	Ahead    int
	Behind   int
	Gone     bool
}

// ReadBranchStatus 读取当前分支、上游引用与领先/落后提交数。
// 用 for-each-ref 一次拿全：%(HEAD) 标出当前分支，%(upstream:track) 给出
// [ahead N, behind M] / [gone]。detached HEAD 没有当前分支，返回零值。
func ReadBranchStatus(repoPath string) (BranchStatus, error) {
	out, err := Run(repoPath, "for-each-ref",
		"--format=%(HEAD)%09%(refname:short)%09%(upstream:short)%09%(upstream:track)",
		"refs/heads")
	if err != nil {
		return BranchStatus{}, err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "*\t") {
			continue
		}
		parts := strings.Split(line, "\t")
		var st BranchStatus
		if len(parts) > 1 {
			st.Branch = parts[1]
		}
		if len(parts) > 2 {
			st.Upstream = parts[2]
		}
		if len(parts) > 3 {
			parseTrack(parts[3], &st)
		}
		return st, nil
	}
	return BranchStatus{}, nil
}

// ReadBranchStatuses 并发读取多个仓库的分支跟踪状态（按仓库路径索引）。
// 单个仓库读取失败按零值处理：下拉里该仓库只显示名字。
func ReadBranchStatuses(repos []Repo) map[string]BranchStatus {
	statuses := mapLimit(repos, 8, func(repo Repo) BranchStatus {
		st, err := ReadBranchStatus(repo.Path)
		if err != nil {
			return BranchStatus{}
		}
		return st
	})
	out := make(map[string]BranchStatus, len(repos))
	for i, repo := range repos {
		out[repo.Path] = statuses[i]
	}
	return out
}

// parseTrack 解析 %(upstream:track)：空串（齐平）、[gone]、
// [ahead N]、[behind M]、[ahead N, behind M]。
func parseTrack(track string, st *BranchStatus) {
	track = strings.TrimSuffix(strings.TrimPrefix(track, "["), "]")
	if track == "gone" {
		st.Gone = true
		return
	}
	for _, part := range strings.Split(track, ", ") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch fields[0] {
		case "ahead":
			st.Ahead = n
		case "behind":
			st.Behind = n
		}
	}
}
