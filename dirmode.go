package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
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
	// showDiff 是目录列表的「查看 diff」开关：勾选后点带改动的文件直接
	// 展示 diff，不勾选（默认）只读查看原内容
	showDiff bool
	file     *fileView
}

// rebuildRows 把目录缓存按展开态拍平成渲染行，并聚合各目录的改动状态。
func (d *dirPane) rebuildRows(root string, changes []git.Change) {
	if d.cache == nil {
		d.cache = map[string][]fsEntry{}
	}
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
			row := sideRow{id: e.abs, label: e.name, depth: depth}
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

// openFile 打开一个工作区文件：「查看 diff」勾选且带改动时直接展示 diff
// （默认全展开），否则只读查看原内容；文件属于某仓库时同步当前仓库。
func (ws *workspace) openFile(abs string) {
	ws.openFileAt(abs, 0)
}

// reloadOpenFile 是 watch 命中后的当前文件重读：不走 openFileAt 重新打开
// ——重新打开会换全新的 fileView（loading 闪断、滚回顶部、Markdown 预览
// 被踢回源码），而这些状态属于已打开的会话；内容是否真的变化由 applyFile
// 判定，相同直接丢弃。路由（「查看 diff」）只属于打开请求，重读不重路由。
func (ws *workspace) reloadOpenFile() {
	if ws.dirs.file == nil {
		return
	}
	abs := ws.dirs.file.abs
	loadAsync(ws, func() fileResult {
		content, err := os.ReadFile(abs)
		return fileResult{content: content, err: err}
	}, func(r fileResult) { ws.applyFile(abs, r.content, r.err) })
}

// openFileAt 是 openFile 的带跳转版本：打开后跳到 line 行（1 起，0 = 从头
// 显示），搜索结果的内容命中靠它定位。
func (ws *workspace) openFileAt(abs string, line int) {
	if repo := ws.repoForAbs(abs); repo != "" {
		ws.switchActiveRepo(repo)
	}
	if ws.dirs.showDiff {
		if ch := ws.changeForAbs(abs); ch != nil {
			ws.openDiffExpandedAt(*ch, line)
			return
		}
	}
	ws.closeDiff() // 主区以最后点击的为准：打开文件即让位出 diff（否则会一直显示旧 diff）
	// 预览模式不记忆：每次打开文件都是源码，用户手动切预览（否则上一个
	// 文件的预览态会带到下一个文件）
	ws.mdPreview = false
	ws.dirs.file = &fileView{abs: abs, loading: true, line: line}
	loadAsync(ws, func() fileResult {
		content, err := os.ReadFile(abs)
		return fileResult{content: content, err: err}
	}, func(r fileResult) { ws.applyFile(abs, r.content, r.err) })
}

// fileResult 是文件读取管线的加载结果。
type fileResult struct {
	content []byte
	err     error
}

// retargetOpened 把主区已打开的内容按「查看 diff」新开关切换：勾选时目录
// 列表打开的只读文件有改动就切到 diff；取消时目录入口打开的 diff 切回只读
// 查看。不属于开关管辖的内容（diff 列表入口的 diff、无改动文件）不动。
func (ws *workspace) retargetOpened() {
	switch {
	case ws.dirs.showDiff:
		if ws.dirs.file != nil && ws.changeForAbs(ws.dirs.file.abs) != nil {
			ws.openFile(ws.dirs.file.abs)
		}
	case ws.diffv.open != nil && ws.diffv.fromDir:
		ws.openFile(ws.diffv.target())
	}
}

// applyFile 应用文件读取结果；期间已关闭/换文件时作废。
func (ws *workspace) applyFile(abs string, content []byte, err error) {
	if ws.dirs.file == nil || ws.dirs.file.abs != abs {
		return // 期间已关闭/换文件
	}
	// watch 重载时内容没变就不动状态：队列投递每次都重建帧，
	// 重复应用相同内容会让界面持续闪烁
	head := content
	if len(head) > 8192 {
		head = head[:8192]
	}
	binary := isBinary(string(head))
	same := err == nil && !ws.dirs.file.loading && ws.dirs.file.kind != "" && ws.dirs.file.content == string(content)
	if same {
		// 内容没变（watch 重载）：仍按新请求跳到目标行
		ws.jumpToFileLine()
		return
	}
	ws.dirs.file.loading = false
	if err != nil {
		ws.setErr("读取文件失败", err.Error())
		return
	}
	// 原始字节全存（文本供 Markdown 预览，二进制供重读比对跳过重解码）
	ws.dirs.file.content = string(content)
	if binary {
		// 二进制：图片扩展名走预览，其余明确提示不支持
		if isImagePath(abs) {
			if bmp, derr := ui.DecodeBitmap(content); derr == nil {
				ws.dirs.file.kind = "image"
				ws.dirs.file.bitmap = bmp
				return
			}
		}
		ws.dirs.file.kind = "unsupported"
		return
	}
	ws.dirs.file.kind = "text"
	// 官方编辑器只读查看：按文件名选语言做语法高亮，未知扩展名为纯文本
	st := &editor.State{
		Buffer:      editor.NewBuffer(string(content)),
		ReadOnly:    true,
		LineNumbers: editor.LineNumbersOn,
	}
	st.Buffer.SetLanguage(editor.LanguageFor(abs))
	ws.dirs.file.ed = st
	if isMarkdownPath(abs) {
		ws.mdOpts.LoadImage = markdown.FileImages(dirOf(abs))
	}
	ws.jumpToFileLine()
}

// jumpToFileLine 把文件视图跳到打开请求指定的行（1 起），跳到即消费：
// 之后的 watch 重载不再把视图拽回原处。
func (ws *workspace) jumpToFileLine() {
	v := ws.dirs.file
	if v == nil || v.ed == nil || v.line <= 0 {
		return
	}
	v.ed.GoTo(editor.Pos{Line: v.line - 1})
	v.line = 0
}

// changeForAbs 找文件对应的改动（abs = RepoPath/Path）；无改动返回 nil
// （走只读查看）。查表：多分区的取舍（工作区侧优先）在索引构建时定死，
// 索引随扫描/合并经 rebuildDerived 重建（buildChangeByAbs）。
func (ws *workspace) changeForAbs(abs string) *git.Change {
	return ws.changeByAbs[abs]
}

// repoForAbs 找覆盖 abs 的最深仓库。
func (ws *workspace) repoForAbs(abs string) string {
	best := ""
	for _, r := range ws.repos {
		if abs == r.Path || strings.HasPrefix(abs, r.Path+"/") {
			if len(r.Path) > len(best) {
				best = r.Path
			}
		}
	}
	return best
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
		if !fsEntriesEqual(ws.dirs.cache[target], entries) {
			ws.dirs.cache[target] = entries
			changed = true
		}
	}
	if changed {
		ws.dirs.rebuildRows(ws.root, ws.changes)
	}
}

func fsEntriesEqual(a, b []fsEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// relPath 显示用：abs 去掉 root 前缀。
func relPath(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}
