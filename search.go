package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// searchPane 是侧栏顶部的全局搜索：搜当前工作区所有仓库的文件名
// （输入即搜）与文件内容（回车搜）。结果按目录层级树展示（命中文件的
// 父目录链是 branch 行，文件嵌在各自目录下），清空输入框恢复原列表。
type searchPane struct {
	open    bool    // 搜索态：搜索框占满侧栏顶行（Segmented 被替换，范围锁定）
	query   string  // 输入框文本（含未提交的）
	active  string  // 已生效的搜索词（空 = 没在搜索，侧栏显示原列表）
	selList selList // 结果列表的选区状态（sel 一律 -1 起步，坑 11）
	seq     int     // 世代：输入连续变化时旧结果作废
	hits    []searchHit
	// contentHits 是回车触发的内容命中（与名字命中合并成一棵结果树）
	contentHits []searchHit
	// contentDone 区分「还没回车搜过」与「搜了没结果」
	contentDone bool
	// contentRunning 是内容搜索在途（回车后、结果回来前）：这段时间不能
	// 显示「没有匹配」——结果还没回来
	contentRunning bool
	// nameDone 是名字搜索已完成（changes 模式同步过滤天然完成；目录列表
	// 模式异步，应用结果时置位）：完成后零命中才显示「没有文件名命中」，
	// 之前显示「搜索中…」——不然零命中词会永远卡在「搜索中…」（实抓）
	nameDone bool
	// focus 是进入搜索态时对输入框的一次性聚焦请求
	focus bool
	// openDirs 记录结果树里 branch 行的折叠状态（缺省=展开）；新搜索时清空
	openDirs map[string]bool
}

// searchHit 是一个命中条目：名字命中（line=0）、目录命中（dir=true）
// 或内容命中（line>0）。
type searchHit struct {
	repoPath string // 所属仓库
	path     string // 仓库相对路径（目录命中是目录的相对路径）
	abs      string
	line     int    // 内容命中行号；名字/目录命中为 0
	text     string // 内容命中的行文本
	dir      bool   // 目录名命中
	code     string // git porcelain 状态码；未改动文件为空
	section  string // unstaged / staged / untracked
}

// onSearchQueryChanged 是输入框 Changed 回调：即时跑名字搜索，内容搜索
// 等回车（贵，逐键触发会拖垮大仓库）。
func (ws *workspace) onSearchQueryChanged() {
	ws.search.seq++
	q := strings.TrimSpace(ws.search.query)
	if q == "" {
		// 词被清空（自带 ⊗ / 删光）：搜索态没有可展示的内容，直接退出
		// 恢复 Segmented（closeSearch 里的状态清理是它的超集）
		ws.closeSearch()
		return
	}
	ws.search.active = q
	ws.search.hits = nil
	ws.search.contentHits = nil
	ws.search.contentDone = false
	ws.search.contentRunning = false
	ws.search.nameDone = false
	ws.search.openDirs = nil
	ws.runNameSearch(q)
}

// runNameSearch 异步搜名字（文件 + 目录）。范围跟随侧栏：diff列表 tab
// 只搜改动文件与它们的目录（集合在内存里，同步过滤）；目录列表 tab 搜
// 全仓库（含未跟踪）。
func (ws *workspace) runNameSearch(word string) {
	seq := ws.search.seq
	wordLower := strings.ToLower(word)
	if ws.sidebarTab == "changes" {
		// diff 列表：改动文件的路径集在内存，直接过滤（含目录段命中）
		seenFile, seenDir := map[string]bool{}, map[string]bool{}
		var hits []searchHit
		for i := range ws.changes {
			c := &ws.changes[i]
			if !seenFile[c.RepoPath+"/"+c.Path] && strings.Contains(strings.ToLower(c.Path), wordLower) {
				seenFile[c.RepoPath+"/"+c.Path] = true
				hits = append(hits, searchHit{repoPath: c.RepoPath, path: c.Path, abs: c.RepoPath + "/" + c.Path, code: c.Code, section: c.Section})
			}
			seg := c.Path
			for {
				idx := strings.LastIndexByte(seg, '/')
				if idx < 0 {
					break
				}
				seg = seg[:idx]
				if !seenDir[c.RepoPath+"/"+seg] && strings.Contains(strings.ToLower(seg), wordLower) {
					seenDir[c.RepoPath+"/"+seg] = true
					hits = append(hits, searchHit{repoPath: c.RepoPath, path: seg, abs: c.RepoPath + "/" + seg, dir: true})
				}
			}
		}
		sortHits(hits)
		ws.search.hits = hits
		ws.search.nameDone = true
		return
	}
	repos := ws.repos
	loadAsync(ws, func() map[string][]string { return git.SearchNamesAll(repos, word) },
		func(byRepo map[string][]string) { ws.applyNameSearch(seq, byRepo, wordLower) })
}

// applyNameSearch 应用目录列表模式的名字命中（世代过期则丢弃）：文件命中
// 之外，目录段命中也单独成行（空目录名命中也有结果，不依赖目录下有命中文件）。
func (ws *workspace) applyNameSearch(seq int, byRepo map[string][]string, wordLower string) {
	if seq != ws.search.seq {
		return
	}
	var hits []searchHit
	seenDir := map[string]bool{}
	for _, r := range ws.repos {
		for _, p := range byRepo[r.Path] {
			// 目录列表模式不带 git 状态：它搜的是全部文件，不是改动集
			hits = append(hits, searchHit{repoPath: r.Path, path: p, abs: r.Path + "/" + p})
			seg := p
			for {
				idx := strings.LastIndexByte(seg, '/')
				if idx < 0 {
					break
				}
				seg = seg[:idx]
				if !seenDir[r.Path+"/"+seg] && strings.Contains(strings.ToLower(seg), wordLower) {
					seenDir[r.Path+"/"+seg] = true
					hits = append(hits, searchHit{repoPath: r.Path, path: seg, abs: r.Path + "/" + seg, dir: true})
				}
			}
		}
	}
	sortHits(hits)
	ws.search.hits = hits
	ws.search.nameDone = true
}

// runContentSearch 是回车回调：搜文件内容。范围跟随侧栏（diff列表 =
// 改动文件；目录列表 = 全仓库）。
func (ws *workspace) runContentSearch() {
	word := strings.TrimSpace(ws.search.active)
	if word == "" {
		return
	}
	seq := ws.search.seq
	repos := ws.repos
	ws.search.contentDone = true
	ws.search.contentRunning = true
	ws.search.contentHits = nil
	ws.app.invalidate()
	var fn func() map[string][]git.ContentHit
	if ws.sidebarTab == "changes" {
		// diff 列表：限定改动文件
		paths := map[string][]string{}
		for i := range ws.changes {
			c := &ws.changes[i]
			paths[c.RepoPath] = append(paths[c.RepoPath], c.Path)
		}
		fn = func() map[string][]git.ContentHit { return git.SearchContentsPathsAll(repos, word, paths) }
	} else {
		fn = func() map[string][]git.ContentHit { return git.SearchContentsAll(repos, word) }
	}
	loadAsync(ws, fn, func(byRepo map[string][]git.ContentHit) { ws.applyContentSearch(seq, byRepo) })
}

// applyContentSearch 应用内容命中（世代过期则丢弃）。
func (ws *workspace) applyContentSearch(seq int, byRepo map[string][]git.ContentHit) {
	if seq != ws.search.seq {
		return
	}
	var hits []searchHit
	for _, r := range ws.repos {
		for _, h := range byRepo[r.Path] {
			hit := searchHit{
				repoPath: r.Path, path: h.Path, abs: r.Path + "/" + h.Path,
				line: h.Line, text: strings.TrimSpace(h.Text),
			}
			// git 状态只在 diff列表模式（改动集范围）有：目录列表搜全部
			// 文件，多数不是改动，行上带零星状态反而像漏标
			if ws.sidebarTab == "changes" {
				if c := ws.changeByAbs[hit.abs]; c != nil {
					hit.code, hit.section = c.Code, c.Section
				}
			}
			hits = append(hits, hit)
		}
	}
	sortHits(hits)
	ws.search.contentHits = hits
	ws.search.contentRunning = false
}

func sortHits(hits []searchHit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].repoPath != hits[j].repoPath {
			return hits[i].repoPath < hits[j].repoPath
		}
		if hits[i].path != hits[j].path {
			return hits[i].path < hits[j].path
		}
		return hits[i].line < hits[j].line
	})
}

// searching 报告侧栏当前是否显示搜索结果。
func (ws *workspace) searching() bool {
	return ws.search.active != ""
}

// openSearch 点搜索 icon 进入搜索态：Segmented 让位给输入框并聚焦。
func (ws *workspace) openSearch() {
	ws.search.open = true
	ws.search.focus = true
}

// closeSearch 点 × 退出搜索态：清空全部搜索状态（词、结果、折叠、选区），
// 恢复 Segmented——下次进入是干净的搜索。
func (ws *workspace) closeSearch() {
	ws.search.open = false
	ws.search.focus = false
	ws.search.query = ""
	ws.search.active = ""
	ws.search.hits = nil
	ws.search.contentHits = nil
	ws.search.contentDone = false
	ws.search.contentRunning = false
	ws.search.nameDone = false
	ws.search.openDirs = nil
	ws.search.selList.sel = -1
	ws.search.seq++
}

// searchTreeNode 是结果树的节点：目录段、文件（叶子或带内容行的 branch）、
// 内容行（叶子）。children 用名字键聚合，拍平时排序（目录在文件前）。
type searchTreeNode struct {
	name    string
	abs     string
	dir     bool
	repo    bool         // 仓库锚行（拍平时不参与单链压缩）
	nameHit bool         // 目录名含搜索词（独立成行，不并入压缩链）
	leaf    *searchHit   // 文件名命中叶子；branch 节点为 nil
	lines   []*searchHit // 该文件的内容命中（branch 的子叶子）
	// children 以 abs 为键：不同仓库的同名目录各自成树，不按名字合并
	children map[string]*searchTreeNode
}

func ensureChild(n *searchTreeNode, name, abs string, dir bool) *searchTreeNode {
	if c, ok := n.children[abs]; ok {
		return c
	}
	c := &searchTreeNode{name: name, abs: abs, dir: dir, children: map[string]*searchTreeNode{}}
	n.children[abs] = c
	return c
}

// buildSearchRows 把命中条目组织成目录层级树并拍平为侧栏行：命中文件的
// 父目录链是 branch 行（默认展开，点击可折叠），文件嵌在各自目录下；
// 文件名命中是叶子，内容命中把文件变成 branch、命中行是它的子叶子。
// open 记录 branch 的折叠状态（缺省=展开），点击 branch 行时翻转。
func buildSearchRows(hits []searchHit, open map[string]bool) []sideRow {
	root := &searchTreeNode{children: map[string]*searchTreeNode{}}
	for i := range hits {
		h := &hits[i]
		// 仓库锚行在最顶层（IDE 式分组：仓库名是 branch，展开是目录链；
		// 拍平时锚行不参与单链压缩）。目录链挂在锚下——挂 root 会让
		// 压缩链与仓库行平级（实抓：无缩进）
		anchor := ensureChild(root, filepath.Base(h.repoPath), h.repoPath, true)
		anchor.repo = true
		segs := strings.Split(h.path, "/")
		abs := h.repoPath
		if h.dir {
			// 目录名命中：挂整条目录链，末段目录标记命中（独立成行，
			// 不依赖目录下有命中文件）
			node := anchor
			for _, seg := range segs {
				abs += "/" + seg
				node = ensureChild(node, seg, abs, true)
			}
			node.nameHit = true
			continue
		}
		node := anchor
		for _, seg := range segs[:len(segs)-1] {
			abs += "/" + seg
			node = ensureChild(node, seg, abs, true)
		}
		name := segs[len(segs)-1]
		if h.line == 0 {
			leaf := ensureChild(node, name, h.abs, false)
			leaf.leaf = h
			continue
		}
		// 内容命中：文件变 branch，命中行挂它下面（行叶子带行号与原文）
		file := ensureChild(node, name, h.abs, false)
		file.lines = append(file.lines, h)
	}
	var rows []sideRow
	flattenSearchNode(root, 0, open, &rows)
	return rows
}

// flattenSearchNode 深度优先拍平一层：目录在文件前，各自按名字排序。
// 目录孩子在拍平前做单链压缩（compactDirChain）：只有一条目录孩子的纯
// 过路段合并成一行（a/b/c），含命中文件或分叉的目录才单独 branch。
func flattenSearchNode(n *searchTreeNode, depth int, open map[string]bool, rows *[]sideRow) {
	var dirs, files []*searchTreeNode
	for _, c := range n.children {
		if c.dir {
			dirs = append(dirs, c)
		} else {
			files = append(files, c)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	for _, c := range dirs {
		if c.repo {
			// 仓库锚：branch 行不压缩，整个仓库的子树在它下面
			appendBranch(c.abs, c.name, depth, false, open, rows, func() {
				flattenSearchNode(c, depth+1, open, rows)
			})
			continue
		}
		if c.nameHit {
			// 目录名命中：独立成行（段名），子层照常展开
			appendBranch(c.abs, c.name, depth, true, open, rows, func() {
				flattenSearchNode(c, depth+1, open, rows)
			})
			continue
		}
		label, terminal := compactDirChain(c)
		appendDirBranch(label, terminal, depth, open, rows)
	}
	for _, c := range files {
		appendFileNode(c, depth, open, rows)
	}
}

// compactDirChain 沿纯目录单链合并名字：链上没有命中文件、没有分叉、
// 没有目录名命中的过路段并成一条（a/b/c）；遇文件、分叉或 nameHit 停。
// 返回合并后的显示名与链尾节点（branch 的折叠状态挂在它身上）。
func compactDirChain(c *searchTreeNode) (string, *searchTreeNode) {
	label := c.name
	node := c
	for node.leaf == nil && len(node.lines) == 0 && !node.nameHit && !node.repo && len(node.children) == 1 {
		var only *searchTreeNode
		for _, ch := range node.children {
			only = ch
		}
		if !only.dir {
			break
		}
		label += "/" + only.name
		node = only
	}
	return label, node
}

// appendDirBranch 追加一条压缩后的目录 branch（label 如 a/b/c），展开时
// 带出链尾的孩子：文件与下一层目录链（分叉处各自的链继续压缩）。
func appendDirBranch(label string, terminal *searchTreeNode, depth int, open map[string]bool, rows *[]sideRow) {
	appendBranch(terminal.abs, label, depth, terminal.nameHit, open, rows, func() {
		var subdirs, subfiles []*searchTreeNode
		for _, c := range terminal.children {
			if c.dir {
				subdirs = append(subdirs, c)
			} else {
				subfiles = append(subfiles, c)
			}
		}
		sort.Slice(subdirs, func(i, j int) bool { return subdirs[i].name < subdirs[j].name })
		sort.Slice(subfiles, func(i, j int) bool { return subfiles[i].name < subfiles[j].name })
		for _, d := range subdirs {
			if d.nameHit {
				appendBranch(d.abs, d.name, depth+1, true, open, rows, func() {
					flattenSearchNode(d, depth+2, open, rows)
				})
				continue
			}
			l2, t2 := compactDirChain(d)
			appendDirBranch(l2, t2, depth+1, open, rows)
		}
		for _, f := range subfiles {
			appendFileNode(f, depth+1, open, rows)
		}
	})
}

// appendFileNode 追加一个文件节点：名字命中是叶子；内容命中是 branch
// （文件名），其子叶子是各命中行（行号 + 行原文）。
func appendFileNode(f *searchTreeNode, depth int, open map[string]bool, rows *[]sideRow) {
	// git 状态：改动文件行显示状态字母与状态色（diff列表模式的搜索结果）
	h := f.leaf
	if h == nil && len(f.lines) > 0 {
		h = f.lines[0]
	}
	letter, rank := "", 0
	if h != nil && h.code != "" {
		letter, rank = statusRank(h.code, h.section)
	}
	if len(f.lines) == 0 {
		*rows = append(*rows, sideRow{id: f.abs, label: f.name, depth: depth, letter: letter, rank: rank})
		return
	}
	appendBranch(f.abs, f.name, depth, false, open, rows, func() {
		for _, h := range f.lines {
			*rows = append(*rows, sideRow{
				id:    fmt.Sprintf("%s:%d", h.abs, h.line),
				label: fmt.Sprintf("%d %s", h.line, h.text),
				depth: depth + 1,
			})
		}
	})
	_ = letter
	_ = rank
}

// appendBranch 追加一条 branch 行并按展开状态执行子层构建。
func appendBranch(id, label string, depth int, nameHit bool, open map[string]bool, rows *[]sideRow, children func()) {
	expanded := true
	if v, seen := open[id]; seen {
		expanded = v
	}
	*rows = append(*rows, sideRow{id: id, label: label, depth: depth, branch: true, nameHit: nameHit})
	if expanded {
		children()
	}
}

// renderSearchResults 渲染搜索结果树（侧栏主体被搜索接管时）。branch 行
// 点击折叠/展开本树；叶子点击打开文件。空态文案跟状态机走：名字搜索在途
// 是「搜索中…」；名字完成零命中、内容未搜是「没有文件名命中」（引导回车）；
// 回车后内容搜索在途仍是「搜索中…」（结果没回来不能说「没有匹配」）；
// 都完成才判「没有匹配」。
func (ws *workspace) renderSearchResults(c *ui.Context) {
	hits := append(append([]searchHit{}, ws.search.hits...), ws.search.contentHits...)
	switch {
	case ws.search.active == "":
		return
	case len(hits) == 0 && !ws.search.contentDone:
		if ws.search.nameDone {
			emptyState(c, "没有文件名命中", "回车搜索文件内容")
		} else {
			emptyState(c, "搜索中…", "")
		}
	case ws.search.contentRunning:
		emptyState(c, "搜索中…", "正在搜索文件内容")
	case len(hits) == 0:
		emptyState(c, "没有匹配", "文件名 / 文件内容都未命中")
	default:
		rows := buildSearchRows(hits, ws.search.openDirs)
		hitByID := map[string]searchHit{}
		for i := range hits {
			h := hits[i]
			hitByID[h.abs] = h
			if h.line > 0 {
				hitByID[fmt.Sprintf("%s:%d", h.abs, h.line)] = h
			}
		}
		if row, ok := treeList(c, &ws.search.selList, "search-list", rows, func(r ui.ListRow) {
			sideRowView(r, rows[r.Index], func() {}, nil)
		}); ok {
			if row.branch {
				// branch 点击：折叠/展开本树节点（treeList 已把选区归 -1）
				if ws.search.openDirs == nil {
					ws.search.openDirs = map[string]bool{}
				}
				prev, seen := ws.search.openDirs[row.id]
				ws.search.openDirs[row.id] = seen && !prev
			} else if h, hit := hitByID[row.id]; hit {
				// 内容命中带行号：打开文件并跳到该行；名字命中从头显示
				ws.openFileAt(h.abs, h.line)
			}
		}
	}
}
