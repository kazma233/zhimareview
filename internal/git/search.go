package git

import (
	"sort"
	"strings"
)

// ContentHit 是内容搜索的一行命中。
type ContentHit struct {
	// Path 仓库相对路径
	Path string
	// Line 1 起的命中行号
	Line int
	// Text 命中行原文
	Text string
}

// SearchNames 返回仓库内（含未跟踪）路径包含词（不分大小写）的文件，
// 按路径排序。目录不单独返回：目录命中由调用方从文件路径的段推导。
func SearchNames(repoPath, word string) []string {
	out, err := Run(repoPath, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil
	}
	word = strings.ToLower(word)
	var hits []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || !strings.Contains(strings.ToLower(line), word) {
			continue
		}
		hits = append(hits, line)
	}
	sort.Strings(hits)
	return hits
}

// grepNoMatch 判断 git grep 的错误是否只是「没有匹配」：无匹配时 git 以
// 非零退出且 stderr 为空（空仓库同样如此），Run 会把它包成普通错误。
func grepNoMatch(err error) bool {
	return err != nil && strings.HasSuffix(err.Error(), "exit status 1")
}

// SearchContentsPaths 在限定的文件列表里搜内容（diff 列表模式：只在改动
// 文件里搜）。行为与 SearchContents 一致；paths 为空返回空。
func SearchContentsPaths(repoPath, word string, paths []string) []ContentHit {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"grep", "-I", "-z", "-n", "-i", "-F", "--untracked", "-e", word, "--"}, paths...)
	out, err := Run(repoPath, args...)
	if err != nil {
		if grepNoMatch(err) {
			return nil
		}
		return nil
	}
	return parseGrep(out)
}

// SearchContents 在仓库内（含未跟踪的文本文件）搜索词，不分大小写、
// 固定子串匹配；二进制文件跳过（-I）。每仓库最多返回 maxContentHits 行。
func SearchContents(repoPath, word string) []ContentHit {
	// -z 用 NUL 分隔 path/line/text，文件名带冒号（workspace 的 go.mod 里
	// 常见）也不会被解析错位
	out, err := Run(repoPath, "grep", "-I", "-z", "-n", "-i", "-F", "--untracked", "-e", word)
	if err != nil {
		if grepNoMatch(err) {
			return nil
		}
		return nil
	}
	return parseGrep(out)
}

// parseGrep 解析 git grep -z 的输出（path\0line\0text\n 逐条）。
func parseGrep(out string) []ContentHit {
	var hits []ContentHit
	for _, rec := range strings.Split(out, "\n") {
		rec = strings.TrimSuffix(rec, "\r")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		line := 0
		for _, d := range parts[1] {
			if d < '0' || d > '9' {
				line = 0
				break
			}
			line = line*10 + int(d-'0')
		}
		if line == 0 {
			continue
		}
		hits = append(hits, ContentHit{Path: parts[0], Line: line, Text: parts[2]})
		if len(hits) >= maxContentHits {
			break
		}
	}
	return hits
}

// maxContentHits 是单仓库内容命中的上限：超大仓库的通用词能命中几万行，
// 全量返回既慢又没有可读性。
const maxContentHits = 200

// SearchNamesAll 并发在所有仓库里搜文件名；repoPath → 命中路径。
func SearchNamesAll(repos []Repo, word string) map[string][]string {
	results := mapLimit(repos, 8, func(r Repo) []string { return SearchNames(r.Path, word) })
	out := make(map[string][]string, len(repos))
	for i, hits := range results {
		if len(hits) > 0 {
			out[repos[i].Path] = hits
		}
	}
	return out
}

// SearchContentsAll 并发在所有仓库里搜内容；repoPath → 命中行。
func SearchContentsAll(repos []Repo, word string) map[string][]ContentHit {
	results := mapLimit(repos, 8, func(r Repo) []ContentHit { return SearchContents(r.Path, word) })
	return collectContentHits(repos, results)
}

// SearchContentsPathsAll 同 SearchContentsAll，但每仓库限定文件列表
// （diff 列表模式：只在改动文件里搜）。
func SearchContentsPathsAll(repos []Repo, word string, byRepo map[string][]string) map[string][]ContentHit {
	results := mapLimit(repos, 8, func(r Repo) []ContentHit {
		return SearchContentsPaths(r.Path, word, byRepo[r.Path])
	})
	return collectContentHits(repos, results)
}

func collectContentHits(repos []Repo, results [][]ContentHit) map[string][]ContentHit {
	out := make(map[string][]ContentHit, len(repos))
	for i, hits := range results {
		if len(hits) > 0 {
			out[repos[i].Path] = hits
		}
	}
	return out
}
