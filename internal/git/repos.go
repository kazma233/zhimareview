package git

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Repo 是仓库发现结果。
type Repo struct {
	// Path 仓库绝对路径（`/` 分隔）
	Path string
	// Name 目录名
	Name string
	// Rel 相对根目录的路径；根仓库为空
	Rel string
}

// skipDirs 只跳过依赖/构建产物目录：它们不会放业务仓库，递归进去只会拖慢扫描。
var skipDirs = map[string]bool{
	"node_modules": true, "target": true, "dist": true,
	"build": true, "vendor": true, "venv": true,
	"__pycache__": true,
}

// SkipDir 是「目录要不要下探/监听」的唯一口径：点开头的隐藏目录
// （.idea/.svn/.gradle/.git 等）加上依赖/构建产物。仓库扫描与文件监听
// 必须共用同一份规则，否则监听侧会为扫描侧认定无业务仓库的目录
// （如 .idea）持续触发重采集。
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || skipDirs[name]
}

// FindRepos 从根目录向下广度优先扫描 git 仓库。
// 每层目录并发读取；找到仓库后不再往里深入（其中的嵌套仓库随子仓库一起管理），
// 根仓库本身继续扫描子目录。单个目录读取失败按空目录处理。
func FindRepos(root string) []Repo {
	root = filepath.ToSlash(root)
	type item struct{ path, rel string }
	var repos []Repo
	frontier := []item{{path: root, rel: ""}}
	for len(frontier) > 0 {
		// 每层并发读目录（上限 16），分层扫描
		entriesPer := mapLimit(frontier, 16, func(it item) []os.DirEntry {
			entries, err := os.ReadDir(it.path)
			if err != nil {
				return nil
			}
			return entries
		})
		// 有 .git 只是「可能」是仓库：空/损坏的 .git 目录会被 git 忽略、
		// 向上解析到父仓库，仅凭名字登记会让父仓库的同一批改动按子仓库再报一遍。
		// 交 git 自己判定（并发上限 8），无效条目按普通目录继续下探。
		var candidates []int
		for i, entries := range entriesPer {
			for _, e := range entries {
				if e.Name() == ".git" {
					candidates = append(candidates, i)
					break
				}
			}
		}
		isRepo := make([]bool, len(frontier))
		for k, ok := range mapLimit(candidates, 8, func(i int) bool {
			return isWorkTreeRoot(frontier[i].path)
		}) {
			isRepo[candidates[k]] = ok
		}
		next := make([]item, 0, len(frontier))
		for i, it := range frontier {
			entries := entriesPer[i]
			if isRepo[i] {
				repos = append(repos, Repo{
					Path: it.path,
					Name: repoName(it),
					Rel:  it.rel,
				})
				if it.rel != "" {
					continue
				}
			}
			for _, e := range entries {
				// 点开头目录（.idea/.svn/.gradle 等）不下探：不会放业务仓库，
				// 只会拖慢扫描（.git 在上面 hasGit 检查里消费，不受影响）
				if !e.IsDir() || SkipDir(e.Name()) {
					continue
				}
				childRel := e.Name()
				if it.rel != "" {
					childRel = it.rel + "/" + e.Name()
				}
				next = append(next, item{path: it.path + "/" + e.Name(), rel: childRel})
			}
		}
		frontier = next
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Rel < repos[j].Rel })
	return repos
}

// isWorkTreeRoot 让 git 判定目录是不是工作树根：`--show-prefix` 为空表示
// 当前目录就是工作树根，普通 .git 目录与 worktree/子模块的 .git 指针走同一
// 判定；报错或前缀非空（目录其实位于上层仓库内部）都不算仓库。
func isWorkTreeRoot(dir string) bool {
	prefix, err := Run(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return false
	}
	return strings.TrimSpace(prefix) == ""
}

// repoName 取目录名；根仓库（rel 为空）取路径末段，取不到（如盘根）退回完整路径
func repoName(it struct{ path, rel string }) string {
	if it.rel != "" {
		parts := strings.Split(it.rel, "/")
		return parts[len(parts)-1]
	}
	parts := strings.Split(it.path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return it.path
}

// mapLimit 受限并发映射：结果按输入顺序排列。
func mapLimit[T, R any](items []T, limit int, fn func(T) R) []R {
	results := make([]R, len(items))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = fn(it)
		}()
	}
	wg.Wait()
	return results
}
