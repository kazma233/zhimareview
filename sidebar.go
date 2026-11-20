package main

import (
	"sort"
	"strings"

	"zhimareview/internal/git"
)

// 状态等级常量：数值单点定义，statusRank 产出、rankColor（theme.go）取色
// 与目录聚合比较共用——镜像双方引常量，写错数值编译期即炸。
// rank 越大越"重"，目录继承最重的子孙状态；0 保留给「无改动」。
const (
	rankNone      = 0
	rankOther     = 1 // 未知状态码
	rankUntracked = 2 // U：未跟踪（状态色与 A 同为绿）
	rankAdd       = 2 // A：新增
	rankRename    = 3 // R：改名
	rankCopy      = 3 // C：复制
	rankMod       = 4 // M：修改
	rankDel       = 5 // D：删除
)

// 状态色：M 橙 / A、U 绿 / D 红 / R、C 天蓝，其余弱化。
func statusRank(code, section string) (letter string, rank int) {
	if section == "untracked" {
		return "U", rankUntracked
	}
	switch code {
	case "M":
		return "M", rankMod
	case "A":
		return "A", rankAdd
	case "D":
		return "D", rankDel
	case "R":
		return "R", rankRename
	case "C":
		return "C", rankCopy
	default:
		return code, rankOther
	}
}

// dirID/repoID/fileID 是改动树行身份的语法单点：目录 cd:<rel>、仓库
// cd-repo:<abs>、文件 cf:<repo>|<path>|<section>。收起集（tree.collapsed）
// 与选区跟随按它索引，跨扫描保持；列表布局行与树文件行同 id（fileID）。
// 目录树行身份是文件绝对路径，无前缀语法。
func dirID(rel string) string  { return "cd:" + rel }
func repoID(abs string) string { return "cd-repo:" + abs }

func fileID(c git.Change) string {
	return "cf:" + c.RepoPath + "|" + c.Path + "|" + c.Section
}

// treeItem 是改动树的一个节点：目录（含仓库节点）或文件。
type treeItem struct {
	id    string
	label string
	// rank 子孙最重的状态等级；0 表示无改动（空目录、干净仓库）
	rank int
	// letter 文件行的状态字母；目录行为空
	letter string
	count  int
	isRepo bool
	change *git.Change
	kids   []*treeItem
}

// sectionLabel 是 diff 头部的分区徽标文案。
func sectionLabel(section string) string {
	switch section {
	case "unstaged":
		return "工作区改动"
	case "staged":
		return "已暂存"
	case "untracked":
		return "未跟踪"
	}
	return section
}

// trieNode 是建树用的中间结构：目录链 + 文件 + 可选的仓库归属。
type trieNode struct {
	dirs  map[string]*trieNode
	files []fileRow
	repo  *git.Repo
}

type fileRow struct {
	change git.Change
	name   string
}

func newTrieNode() *trieNode {
	return &trieNode{dirs: map[string]*trieNode{}}
}

// buildChangesTree 跨仓库聚合改动：仓库按 rel 折叠成目录层级，改动文件挂各自仓库下，
// 没有改动的仓库也入树（空节点）。根仓库（rel 为空）不建目录层，由顶层单行承载。
// 返回顶层节点与 id → 节点索引（Outline 的 children 查找用）。
func buildChangesTree(repos []git.Repo, changes []git.Change) ([]*treeItem, map[string]*treeItem) {
	root := newTrieNode()

	// 先铺所有仓库，保证干净仓库也出现在树里
	for i := range repos {
		repo := &repos[i]
		if repo.Rel == "" {
			root.repo = repo
			continue
		}
		node := root
		for _, seg := range strings.Split(repo.Rel, "/") {
			next, ok := node.dirs[seg]
			if !ok {
				next = newTrieNode()
				node.dirs[seg] = next
			}
			node = next
		}
		node.repo = repo
	}

	for _, change := range changes {
		node := root
		found := true
		if change.RepoRel != "" {
			for _, seg := range strings.Split(change.RepoRel, "/") {
				next, ok := node.dirs[seg]
				if !ok {
					found = false
					break
				}
				node = next
			}
		}
		if !found {
			continue
		}
		segments := strings.Split(change.Path, "/")
		for _, seg := range segments[:len(segments)-1] {
			next, ok := node.dirs[seg]
			if !ok {
				next = newTrieNode()
				node.dirs[seg] = next
			}
			node = next
		}
		node.files = append(node.files, fileRow{change: change, name: segments[len(segments)-1]})
	}

	index := map[string]*treeItem{}
	var build func(node *trieNode, dirPath string) *treeItem
	build = func(node *trieNode, dirPath string) *treeItem {
		item := &treeItem{}
		for _, name := range sortedDirNames(node) {
			childPath := name
			if dirPath != "" {
				childPath = dirPath + "/" + name
			}
			child := build(node.dirs[name], childPath)
			child.label = name
			// 仓库节点与普通目录节点分开标记，便于空仓库显示为普通文字
			if r := node.dirs[name].repo; r != nil {
				child.id = repoID(r.Path)
				child.isRepo = true
				if child.count == 0 {
					child.rank = 0
				}
			} else {
				child.id = dirID(childPath)
			}
			index[child.id] = child
			item.kids = append(item.kids, child)
			item.count += child.count
			if child.rank > item.rank {
				item.rank = child.rank
			}
		}
		for _, file := range sortedFileRows(node) {
			letter, rank := statusRank(file.change.Code, file.change.Section)
			fi := &treeItem{
				id:     fileID(file.change),
				label:  file.name,
				rank:   rank,
				letter: letter,
				count:  1,
				change: &file.change,
			}
			index[fi.id] = fi
			item.kids = append(item.kids, fi)
			item.count += 1
			if rank > item.rank {
				item.rank = rank
			}
		}
		return item
	}

	tree := build(root, "")
	for i := range tree.kids {
		tree.kids[i] = compressDirChain(tree.kids[i])
	}
	if root.repo != nil {
		id := repoID(root.repo.Path)
		item := &treeItem{
			id:     id,
			label:  root.repo.Name,
			isRepo: true,
			rank:   tree.rank,
			count:  tree.count,
			kids:   tree.kids,
		}
		if item.count == 0 {
			item.rank = 0
		}
		index[id] = item
		return []*treeItem{item}, index
	}
	return tree.kids, index
}

// compressDirChain 把「只有单个目录孩子、自身无文件」的目录链合并成一行，
// label 显示 a/b/c 路径；行 id 与聚合取链最深层（展开态/选中跟随它）。
// 仓库节点与文件不参与合并——进哪个仓库、文件挂哪层保持原样。
func compressDirChain(it *treeItem) *treeItem {
	for it.change == nil && !it.isRepo && len(it.kids) == 1 &&
		it.kids[0].change == nil && !it.kids[0].isRepo {
		kid := it.kids[0]
		it.label = it.label + "/" + kid.label
		it.id = kid.id
		it.kids = kid.kids
	}
	for i := range it.kids {
		it.kids[i] = compressDirChain(it.kids[i])
	}
	return it
}

// changeRow 是改动树拍平后的渲染行：目录节点（open 展开态）或文件节点。
type changeRow struct {
	item  *treeItem
	depth int
	open  bool
}

// sortChanges 是列表布局的排序契约：仓库相对路径 → 文件路径 → 分区。
func sortChanges(changes []git.Change) []git.Change {
	out := make([]git.Change, len(changes))
	copy(out, changes)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.RepoRel != b.RepoRel {
			return a.RepoRel < b.RepoRel
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Section < b.Section
	})
	return out
}

func sortedDirNames(node *trieNode) []string {
	names := make([]string, 0, len(node.dirs))
	for name := range node.dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedFileRows(node *trieNode) []fileRow {
	rows := make([]fileRow, len(node.files))
	copy(rows, node.files)
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

// changeTreePane 是改动侧栏（层级树与列表布局）的视图状态。唯一写点：
// tree.rebuild/rebuildRows（扫描与合并后）、toggleChange 的收起集合、
// 渲染帧里的两套选区（selList 经 newSelList 构造，坑 11 的 -1 起步由
// selList 承载；branch 行点击后归 -1 已收进 treeList）。
type changeTreePane struct {
	roots []*treeItem
	index map[string]*treeItem
	// collapsed 只记用户显式收起的 branch 条目（id → 收起）：改动树默认
	// 全展开，缺省（不在集合里）= 展开，扫描新增目录自动展开，跨扫描按 id 保持
	collapsed map[string]bool
	rows      []sideRow
	list      selList
	flat      []git.Change
	flatRows  []sideRow
	flatList  selList
	layout    bool // true=列表布局
}

// rebuild 按模型重建层级树与列表布局的全部派生（rebuildDerived 编排调用）。
func (t *changeTreePane) rebuild(repos []git.Repo, changes []git.Change) {
	t.roots, t.index = buildChangesTree(repos, changes)
	t.rebuildRows()
	t.flat = sortChanges(changes)
	t.flatRows = t.flatChangeRows(repos)
}

// rebuildRows 把改动树按展开态拍平成渲染行；聚合（rank/count）已在
// buildChangesTree 算好，这里只做前序遍历。在 rebuild 与开合动作后调用，
// 不在每帧重建。
func (t *changeTreePane) rebuildRows() {
	t.rows = nil
	var walk func(items []*treeItem, depth int)
	walk = func(items []*treeItem, depth int) {
		for _, it := range items {
			branch := it.change == nil
			row := sideRow{
				id:     it.id,
				label:  it.label,
				depth:  depth,
				branch: branch,
				open:   branch && !t.collapsed[it.id],
				rank:   it.rank,
				count:  it.count,
				letter: it.letter,
			}
			t.rows = append(t.rows, row)
			// 收起的 branch 行子树不入行
			if row.open {
				walk(it.kids, depth+1)
			}
		}
	}
	walk(t.roots, 0)
}

// toggleChange 开合一个 branch 条目：展开=从收起集合里删掉（回到缺省展开），
// 收起=记入集合。收起集合在首个写点懒初始化（测试直构不经 chooseDir）。
func (ws *workspace) toggleChange(id string) {
	if ws.tree.collapsed == nil {
		ws.tree.collapsed = map[string]bool{}
	}
	if ws.tree.collapsed[id] {
		delete(ws.tree.collapsed, id)
	} else {
		ws.tree.collapsed[id] = true
	}
	ws.tree.rebuildRows()
}

// flatChangeRows 是列表布局的渲染行：全叶子、零缩进，名称为仓库相对完整路径
// （根仓库没有 rel，用仓库显示名）。
func (t *changeTreePane) flatChangeRows(repos []git.Repo) []sideRow {
	rows := make([]sideRow, len(t.flat))
	for i, ch := range t.flat {
		letter, rank := statusRank(ch.Code, ch.Section)
		rel := ch.RepoRel
		if rel == "" {
			rel = repoDisplayName(repoAt(repos, ch.RepoPath))
		}
		rows[i] = sideRow{
			id:     fileID(ch),
			label:  rel + "/" + ch.Path,
			letter: letter,
			rank:   rank,
		}
	}
	return rows
}
