package git

import (
	"strconv"
	"strings"
)

// CommitRow 是提交列表的一行。Refs 是分支/标签等引用（已去掉两侧括号）；
// Merge 报告是否为合并提交（多父）。
type CommitRow struct {
	Hash      string
	ShortHash string
	Author    string
	Date      string
	Refs      string
	Subject   string
	Merge     bool
}

// logSEP 是 pretty format 的字段分隔符（ASCII 单元分隔符，不会出现在单行消息里）。
const logSEP = "\x1f"

// LogCommits 返回引用可达的提交（按时间倒序）：skip 跳过最前的 N 条，
// limit 限制条数（<=0 不限制）。用于提交历史分页加载。
func LogCommits(repoPath string, skip, limit int) ([]CommitRow, error) {
	args := []string{"log", "--all", "--date=short"}
	if skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(skip))
	}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args,
		"--pretty=format:%H"+logSEP+"%h"+logSEP+"%an"+logSEP+"%ad"+logSEP+"%d"+logSEP+"%s"+logSEP+"%P")
	out, err := Run(repoPath, args...)
	if err != nil {
		return nil, err
	}
	var rows []CommitRow
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, logSEP)
		if len(parts) < 6 {
			continue
		}
		rows = append(rows, CommitRow{
			Hash:      parts[0],
			ShortHash: parts[1],
			Author:    parts[2],
			Date:      parts[3],
			Refs:      strings.TrimSuffix(strings.TrimPrefix(parts[4], " ("), ")"),
			Subject:   parts[5],
			// %P 父哈希以空格分隔：多于一个父即合并提交；旧格式无此字段按非合并处理
			Merge: len(parts) >= 7 && strings.Contains(strings.TrimSpace(parts[6]), " "),
		})
	}
	return rows, nil
}

// CommitPatch 返回单个提交的补丁（不带提交说明头）。
func CommitPatch(repoPath, hash string) (string, error) {
	return Run(repoPath, "show", "--format=", "--patch", hash)
}

// IsMergeCommit 判断提交是否为合并提交：revert 需要 -m，第一版明确不支持。
func IsMergeCommit(repoPath, hash string) (bool, error) {
	out, err := Run(repoPath, "rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return false, err
	}
	return len(strings.Fields(strings.TrimSpace(out))) > 2, nil
}

// RevertCommit 生成反向提交撤销指定提交（--no-edit 不弹编辑器）。
func RevertCommit(repoPath, hash string) error {
	_, err := runWriteGit(repoPath, "revert", "--no-edit", hash)
	return err
}
