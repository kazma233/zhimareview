package main

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
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

// dirID/repoID/fileID 是改动树行身份的语法单点：目录 cd:<section>:<rel>、
// 仓库 cd-repo:<section>:<abs>、文件 cf:<repo>|<path>|<section>。分组后
// 同一目录/仓库在两个分区组里各有一行，id 带分区保证 List 的 Key 唯一；
// 收起集（tree.collapsed）按 id 索引，跨扫描保持，分组内独立开合。
// section 为空（无分组构建，测试用）时保持旧语法。
func dirID(section, rel string) string {
	if section == "" {
		return "cd:" + rel
	}
	return "cd:" + section + ":" + rel
}

func repoID(section, abs string) string {
	if section == "" {
		return "cd-repo:" + abs
	}
	return "cd-repo:" + section + ":" + abs
}

func fileID(c git.Change) string {
	return "cf:" + c.RepoPath + "|" + c.Path + "|" + c.Section
}

// 分区组：diff 列表按 git 状态分成两个连续区段（已暂存在上，未暂存/
// 未跟踪在下），组标题行可点击折叠，组内保持层级树或平铺布局。
const (
	groupStaged   = "staged"
	groupUnstaged = "unstaged"
)

func groupID(section string) string { return "group:" + section }

func groupLabel(section string) string {
	if section == groupStaged {
		return "已暂存"
	}
	return "未暂存"
}

// treeItem 是改动树的一个节点：目录（含仓库节点）或文件。
type treeItem struct {
	id    string
	label string
	// abs 是节点代表路径的绝对值：文件为 RepoPath/Path，目录为扫描根或
	// 仓库根拼出的路径（跨仓库的虚拟目录层没有真实文件路径，仅供展示）
	abs string
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
// scanRoot 是扫描根绝对路径，用于给目录节点拼绝对路径；section 是分区组名，
// 参与目录/仓库行 id（同目录在两个组里各有一行）。调用方已按分区过滤 changes
// 与 repos（干净仓库只进入有改动的组）。
// 返回顶层节点与 id → 节点索引（Outline 的 children 查找用）。
func buildChangesTree(scanRoot string, repos []git.Repo, changes []git.Change, section string) ([]*treeItem, map[string]*treeItem) {
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
	var build func(node *trieNode, dirPath, abs string) *treeItem
	build = func(node *trieNode, dirPath, abs string) *treeItem {
		item := &treeItem{abs: abs}
		for _, name := range sortedDirNames(node) {
			childPath := name
			if dirPath != "" {
				childPath = dirPath + "/" + name
			}
			childAbs := abs + "/" + name
			// 仓库节点以仓库自身路径为准：rel 可能经过不属于任何仓库的虚拟层
			if r := node.dirs[name].repo; r != nil {
				childAbs = r.Path
			}
			child := build(node.dirs[name], childPath, childAbs)
			child.label = name
			// 仓库节点与普通目录节点分开标记，便于空仓库显示为普通文字
			if r := node.dirs[name].repo; r != nil {
				child.id = repoID(section, r.Path)
				child.isRepo = true
				if child.count == 0 {
					child.rank = 0
				}
			} else {
				child.id = dirID(section, childPath)
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
				abs:    changeAbs(file.change),
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

	tree := build(root, "", scanRoot)
	for i := range tree.kids {
		tree.kids[i] = compressDirChain(tree.kids[i])
	}
	if root.repo != nil {
		id := repoID(section, root.repo.Path)
		item := &treeItem{
			id:     id,
			label:  root.repo.Name,
			abs:    root.repo.Path,
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
		it.abs = kid.abs
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

// changeGroup 是一个分区组（已暂存/未暂存）的改动派生：组内独立建树、
// 独立平铺排序。untracked 归未暂存组（与 VS Code 的「更改」一致）。
type changeGroup struct {
	section  string
	roots    []*treeItem
	flat     []git.Change
	flatRows []sideRow // 组内平铺行（不含组标题）
}

// changeTreePane 是改动侧栏（层级树与列表布局）的视图状态。唯一写点：
// tree.rebuild/rebuildRows（扫描与合并后）、toggleChange 的收起集合、
// toggleGroup 的组折叠、渲染帧里的两套选区（selList 经 newSelList 构造，
// 坑 11 的 -1 起步由 selList 承载；行点击后归 -1 已收进 treeList，选中
// 高亮按打开中的会话由 openDiffID 给出）。
type changeTreePane struct {
	// groups 固定两组，顺序即渲染顺序：已暂存在上、未暂存在下
	groups [2]changeGroup
	// index 合并两组的节点索引（目录/仓库 id 带分区，键唯一）
	index map[string]*treeItem
	// collapsed 只记用户显式收起的 branch 条目（id → 收起）：改动树默认
	// 全展开，缺省（不在集合里）= 展开，扫描新增目录自动展开，跨扫描按 id 保持
	collapsed map[string]bool
	// groupCollapsed 记用户显式收起的组标题（group:<section>）：缺省展开
	groupCollapsed map[string]bool
	rows           []sideRow
	list           selList
	flatRows       []sideRow
	flatList       selList
	layout         bool // true=列表布局
}

// rebuild 按模型重建两组改动树与列表布局的全部派生（rebuildDerived 编排调用）。
func (t *changeTreePane) rebuild(root string, repos []git.Repo, changes []git.Change) {
	t.groups = [2]changeGroup{{section: groupStaged}, {section: groupUnstaged}}
	t.index = map[string]*treeItem{}
	for gi := range t.groups {
		g := &t.groups[gi]
		g.flat = sortChanges(sectionChanges(changes, g.section))
		// 干净仓库不再作为空节点出现：只把它们铺进有改动的组，且只铺该组
		// changes 涉及的仓库——两组各铺一份会重复，也不符合分组语义
		var idx map[string]*treeItem
		g.roots, idx = buildChangesTree(root, reposWithChanges(repos, g.flat), g.flat, g.section)
		for id, it := range idx {
			t.index[id] = it
		}
		g.flatRows = flatChangeRows(repos, g.flat)
	}
	t.rebuildRows()
}

// sectionChanges 过滤出一个分区组的改动：已暂存组只收 staged，未暂存组收
// 其余（unstaged 与 untracked）。
func sectionChanges(changes []git.Change, section string) []git.Change {
	var out []git.Change
	for _, c := range changes {
		if section == groupStaged {
			if c.Section != groupStaged {
				continue
			}
		} else if c.Section == groupStaged {
			continue
		}
		out = append(out, c)
	}
	return out
}

// reposWithChanges 只保留该组改动涉及的仓库：分组后干净仓库不再显示为空
// 节点（否则同一批干净仓库会在两个组里重复出现）。
func reposWithChanges(repos []git.Repo, changes []git.Change) []git.Repo {
	used := map[string]bool{}
	for _, c := range changes {
		used[c.RepoPath] = true
	}
	out := make([]git.Repo, 0, len(repos))
	for _, r := range repos {
		if used[r.Path] {
			out = append(out, r)
		}
	}
	return out
}

// groupRows 生成组标题行（两套布局共用）。
func (t *changeTreePane) groupRows(g *changeGroup) sideRow {
	return sideRow{
		id:     groupID(g.section),
		label:  groupLabel(g.section),
		header: true,
		open:   !t.groupCollapsed[groupID(g.section)],
		count:  len(g.flat),
	}
}

// rebuildRows 把两组改动树按展开态拍平成渲染行（组标题 + 组内行）；
// 聚合（rank/count）已在 buildChangesTree 算好，这里只做前序遍历。
// 在 rebuild、目录开合与组折叠后调用，不在每帧重建。
func (t *changeTreePane) rebuildRows() {
	t.rows = nil
	var walk func(items []*treeItem, depth int)
	walk = func(items []*treeItem, depth int) {
		for _, it := range items {
			branch := it.change == nil
			row := sideRow{
				id:     it.id,
				label:  it.label,
				abs:    it.abs,
				change: it.change,
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
	for gi := range t.groups {
		g := &t.groups[gi]
		if len(g.flat) == 0 {
			continue // 空组不显示标题
		}
		t.rows = append(t.rows, t.groupRows(g))
		if t.groupCollapsed[groupID(g.section)] {
			continue
		}
		walk(g.roots, 0)
	}
	t.rebuildFlatRows()
}

// rebuildFlatRows 拼平铺布局行：组标题 + 组内文件行（组折叠时只留标题）。
func (t *changeTreePane) rebuildFlatRows() {
	t.flatRows = nil
	for gi := range t.groups {
		g := &t.groups[gi]
		if len(g.flat) == 0 {
			continue
		}
		t.flatRows = append(t.flatRows, t.groupRows(g))
		if !t.groupCollapsed[groupID(g.section)] {
			t.flatRows = append(t.flatRows, g.flatRows...)
		}
	}
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

// toggleGroup 开合一个分区组（点组标题行）：组内行整体收起/展开，
// 组标题保持可见。
func (ws *workspace) toggleGroup(id string) {
	if ws.tree.groupCollapsed == nil {
		ws.tree.groupCollapsed = map[string]bool{}
	}
	if ws.tree.groupCollapsed[id] {
		delete(ws.tree.groupCollapsed, id)
	} else {
		ws.tree.groupCollapsed[id] = true
	}
	ws.tree.rebuildRows()
}

// flatChangeRows 是列表布局的渲染行：全叶子、零缩进，名称为仓库相对完整路径
// （根仓库没有 rel，用仓库显示名）。
func flatChangeRows(repos []git.Repo, changes []git.Change) []sideRow {
	rows := make([]sideRow, len(changes))
	for i, ch := range changes {
		letter, rank := statusRank(ch.Code, ch.Section)
		rel := ch.RepoRel
		if rel == "" {
			rel = repoDisplayName(repoAt(repos, ch.RepoPath))
		}
		rows[i] = sideRow{
			id:     fileID(ch),
			label:  rel + "/" + ch.Path,
			abs:    changeAbs(ch),
			change: &changes[i],
			letter: letter,
			rank:   rank,
		}
	}
	return rows
}

// baseName 取绝对路径最后一段（「复制名称」的目标）。
func baseName(abs string) string {
	if i := strings.LastIndex(abs, "/"); i >= 0 {
		return abs[i+1:]
	}
	return abs
}

// rowMenu 是侧栏行的右键菜单：行携带的语义决定菜单项，三个列表共用。
// 文件行（带 change）可暂存/取消暂存；目录/仓库行只在归属单一仓库且有
// 改动时提供目录暂存——跨仓库的目录（仓库 rel 的虚拟父层、包含多个仓库
// 的文件系统目录）没有可表达的 git 路径。abs 为空的行（搜索结果）没有菜单。
func (ws *workspace) rowMenu(c *ui.Context, row sideRow) func(m *ui.Menu) {
	if row.abs == "" {
		return nil
	}
	return func(m *ui.Menu) {
		if row.change != nil {
			label := "暂存"
			if row.change.Section == "staged" {
				label = "取消暂存"
			}
			if m.Item(label).Chosen() {
				ws.toggleStageFile(*row.change)
			}
		} else if row.branch && row.count > 0 {
			if repo := ws.repoForAbs(row.abs); repo != "" {
				label := "暂存目录"
				if repo == row.abs {
					label = "暂存仓库"
				}
				if m.Item(label).Chosen() {
					ws.stageDir(row.abs)
				}
			}
		}
		if m.Item("复制路径").Chosen() {
			c.WriteClipboard(row.abs)
		}
		if m.Item("复制名称").Chosen() {
			c.WriteClipboard(baseName(row.abs))
		}
	}
}
