package main

import (
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"

	"zhimareview/internal/git"
)

// fsEntry 是目录树的一项。
type fsEntry struct {
	name   string
	abs    string
	folder bool
}

// readDirEntries 读一层目录：跳过符号链接与 .git，目录在前文件在后，各自按名称排序。
// 读不了（权限、消失中的目录）按空目录处理，不中断整个树。
func readDirEntries(dir string) []fsEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []fsEntry{}
	}
	var folders, files []fsEntry
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		// .git 是仓库内部目录（worktree 里也可能是文件），浏览无意义，默认不展示
		if e.Name() == ".git" {
			continue
		}
		abs := dir + "/" + e.Name()
		if e.IsDir() {
			folders = append(folders, fsEntry{e.Name(), abs, true})
		} else {
			files = append(files, fsEntry{e.Name(), abs, false})
		}
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].name < folders[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return append(folders, files...)
}

// loadDir 懒加载一个目录的直接子项；已有缓存或正在加载时不重复。
func (ws *workspace) loadDir(abs string) {
	if _, cached := ws.dirs.cache[abs]; cached || ws.dirs.loading[abs] {
		return
	}
	ws.dirs.loading[abs] = true
	loadAsync(ws, func() []fsEntry { return readDirEntries(abs) },
		func(entries []fsEntry) { ws.applyDir(abs, entries) })
}

// applyDir 应用目录读取结果并重建目录行。
func (ws *workspace) applyDir(abs string, entries []fsEntry) {
	ws.dirs.loading[abs] = false
	ws.dirs.cache[abs] = entries
	ws.dirs.rebuildRows(ws.root, ws.changes)
}

// toggleDir 展开/收起目录；无缓存先触发懒加载（到货后重建行）。
func (ws *workspace) toggleDir(abs string) {
	if ws.dirs.expanded[abs] {
		ws.dirs.expanded[abs] = false
		ws.dirs.rebuildRows(ws.root, ws.changes)
		return
	}
	ws.dirs.expanded[abs] = true
	ws.loadDir(abs)
	ws.dirs.rebuildRows(ws.root, ws.changes)
}

// dirPane 是目录侧栏与文件查看的状态。唯一写点：loadDir/applyDir/toggleDir
// 的缓存与展开态、rebuildRows 的拍平行、openFile/applyFile 的文件态、
// 渲染帧里的选区（selList 经 newSelList 构造，坑 11 的 -1 起步由 selList 承载）。
type dirPane struct {
	rootLoaded string
	cache      map[string][]fsEntry
	loading    map[string]bool
	expanded   map[string]bool
	rows       []sideRow
	list       selList
	file       *fileView
}

// rebuildRows 把目录缓存按展开态拍平成渲染行，并聚合各目录的改动状态。
func (d *dirPane) rebuildRows(root string, changes []git.Change) {
	if d.cache == nil {
		d.cache = map[string][]fsEntry{}
	}
	// 文件行的暂存入口按「工作区侧优先」取改动，与 openFile 的「查看 diff」
	// 同一索引规则（多分区同文件只留一份）
	byAbs := buildChangeByAbs(changes)
	// abs → 最重改动状态与数量（前缀匹配）
	agg := map[string]*dirAgg{}
	addAgg := func(dir string, rank int) {
		a, ok := agg[dir]
		if !ok {
			a = &dirAgg{}
			agg[dir] = a
		}
		a.count++
		if rank > a.rank {
			a.rank = rank
		}
	}
	for _, c := range changes {
		abs := changeAbs(c)
		_, rank := statusRank(c.Code, c.Section)
		// 文件自身与其逐级父目录都吃这条改动的聚合
		addAgg(abs, rank)
		for dir := parentDir(abs); dir != ""; dir = parentDir(dir) {
			addAgg(dir, rank)
		}
	}

	d.rows = nil
	var walk func(abs string, depth int)
	walk = func(abs string, depth int) {
		for _, e := range d.cache[abs] {
			row := sideRow{id: e.abs, label: e.name, abs: e.abs, depth: depth}
			if e.folder {
				// 直接读值（不存在的 key 零值即 false）；不能写成 `_, open :=`
				// ——那是存在性，收起后 key 仍在，open 恒为 true，永远收不起
				row.branch = true
				row.open = d.expanded[e.abs] && d.cache[e.abs] != nil
				if a, ok := agg[e.abs]; ok {
					row.rank, row.count = a.rank, a.count
				}
				d.rows = append(d.rows, row)
				if row.open {
					walk(e.abs, depth+1)
				}
			} else {
				if a, ok := agg[e.abs]; ok {
					row.rank = a.rank
				}
				row.change = byAbs[e.abs]
				d.rows = append(d.rows, row)
			}
		}
	}
	walk(root, 0)
}

// dirAgg 是目录聚合的改动状态。
type dirAgg struct {
	rank  int
	count int
}

// parentDir 上溯父目录；到根返回 ""。
func parentDir(dir string) string {
	idx := strings.LastIndex(dir, "/")
	if idx <= 0 {
		return ""
	}
	return dir[:idx]
}

// reloadDirs 重读受影响的已展开目录（watch 事件联动）；
// 目录内容没变的跳过，避免无变化的重绘。
func (ws *workspace) reloadDirs(targets []string) {
	changed := false
	for _, target := range targets {
		if _, cached := ws.dirs.cache[target]; !cached {
			continue
		}
		entries := readDirEntries(target)
		if !slices.Equal(ws.dirs.cache[target], entries) {
			ws.dirs.cache[target] = entries
			changed = true
		}
	}
	if changed {
		ws.dirs.rebuildRows(ws.root, ws.changes)
	}
}
