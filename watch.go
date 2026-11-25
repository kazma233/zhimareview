package main

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"zhimareview/internal/git"
)

// watchIgnorePath 监听排除判定：按路径的任意段套用 git.SkipDir 口径。
// 嵌套仓库的 .git（repo/.git 之类）不在首段，漏排会让 git status 写 index
// 触发新 hint，形成「采集 → 写 index → hint → 再采集」的无限回环（持续闪烁）；
// .idea 等编辑器高频写入的隐藏目录同理不监听，与仓库扫描共用同一份规则。
func watchIgnorePath(path string, _ bool) bool {
	for _, seg := range strings.Split(path, "/") {
		if git.SkipDir(seg) {
			return true
		}
	}
	return false
}

// watchSched 内聚 watch 事件批的两条时间规则：事件合批与「.」全量提示的
// 1 秒限流。时间由驱动注入（flush 的 now）：真实循环用系统时钟，测试手动
// 拨钟——限流窗口与合批顺序无需实时等待即可断言。
type watchSched struct {
	pending       []string
	lastFullFlush time.Time
}

// push 把一批事件路径并入待处理队列。
func (s *watchSched) push(paths []string) {
	s.pending = append(s.pending, paths...)
}

// flush 取出待处理批；无批返回 false。「.」全量提示限流：处理已经很贵
// （全仓库重采集 + 打开内容重载），系统缓冲抖动时会连续到达，1 秒内只
// 放行一次，限流丢弃整批；普通批不受限流。lastFullFlush 只随放行的
// 「.」批更新——普通批放行不续延限流窗口。
func (s *watchSched) flush(now time.Time) ([]string, bool) {
	if len(s.pending) == 0 {
		return nil, false
	}
	paths := s.pending
	s.pending = nil
	if pathsContainAll(paths) {
		if now.Sub(s.lastFullFlush) < time.Second {
			return nil, false
		}
		s.lastFullFlush = now
	}
	return paths, true
}

// startWatch 启动根目录递归监听与仓库结构兜底；换目录或窗口关闭时停止。
// HintsOnly 让 Windows/macOS 用单个树句柄监听（无 per-entry 快照），避开
// 非 HintsOnly 模式 8192 条目的 ErrWatchLimit——大根目录必踩。代价是事件
// 粗化为目录级 hint（"." 表示全量），而本应用的刷新本就是「提示后重读」
// 模型（收到事件重新采集 status），粗粒度无损。
// 事件批已由 WatchFiles 的 Debounce/MaxDelay 合并（50ms/500ms 上限），
// 经 300ms tick 合批上报，避免每批 hint 立刻触发一次帧重建。
func (ws *workspace) startWatch() {
	ws.stopWatch()
	root := ws.root
	if root == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ws.watchCancel = cancel

	go func() {
		w, err := mygo.WatchFiles(ctx, root, mygo.FileWatchOptions{
			Recursive: true,
			HintsOnly: true,
			Ignore:    watchIgnorePath,
		})
		if err != nil {
			if ws.post != nil {
				ws.post(func() { ws.watchErr(err) })
			}
			return
		}
		defer w.Close()

		// Next 阻塞拉取进 channel，主循环按 300ms 节流合并上报——
		// 直通会让每批 hint 立刻触发一次帧重建，系统抖动时界面持续闪烁。
		eventsCh := make(chan []mygo.FileEvent, 64)
		go func() {
			for {
				events, err := w.Next(ctx)
				if err != nil {
					close(eventsCh)
					return
				}
				select {
				case eventsCh <- events:
				case <-ctx.Done():
					return
				}
			}
		}()

		var sched watchSched
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case events, ok := <-eventsCh:
				if !ok {
					return
				}
				sched.push(watchEventPaths(events))
			case <-ticker.C:
				paths, ok := sched.flush(time.Now())
				if !ok || ws.post == nil {
					continue
				}
				ws.post(func() { ws.handleFsEvents(root, paths) })
			case <-ctx.Done():
				return
			}
		}
	}()

	// 仓库增删没有可靠的单事件，保留低频兜底扫描（15s）
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				found := git.FindRepos(ws.root)
				if ws.post == nil {
					continue
				}
				ws.post(func() {
					if ws.structureChanged(found) {
						ws.rescan(ws.root)
					}
				})
			}
		}
	}()
}

// stopWatch 停止监听与兜底轮询。
func (ws *workspace) stopWatch() {
	if ws.watchCancel != nil {
		ws.watchCancel()
		ws.watchCancel = nil
	}
}

// watchEventPaths 归一化事件路径（相对 root，/ 分隔）。"." 是系统丢失变更后的
// 全量提示，保留并交由 hitHint 展开为全命中；worktree 的 .git 文件变化兜底过滤。
func watchEventPaths(events []mygo.FileEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		p := strings.ReplaceAll(e.Path, "\\", "/")
		if p == "" {
			continue
		}
		if p != "." &&
			(p == ".git" || strings.HasPrefix(p, ".git/") ||
				strings.HasSuffix(p, "/.git") || strings.Contains(p, "/.git/")) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// hitHint 判断事件提示与路径是否落在同一子树（双向）：hint 为 "." 表示
// 整个根目录变化（全命中）；否则按 root 解析成绝对路径后，hint 覆盖 abs
// （事件粗于目标：目录 hint 命中其下的打开文件/已加载目录）或 abs 覆盖
// hint（事件细于目标：文件事件命中其所在目录与所在仓库）任一方向即命中。
// HintsOnly 模式的事件是目录级提示，不精确到文件。
func hitHint(root, hint, abs string) bool {
	if hint == "." || hint == "" {
		return true
	}
	full := root + "/" + hint
	return abs == full ||
		strings.HasPrefix(abs, full+"/") ||
		strings.HasPrefix(full, abs+"/")
}

// pathsContainAll 是否包含 "."（系统丢失变更后的全量提示）。
func pathsContainAll(paths []string) bool {
	for _, p := range paths {
		if p == "." {
			return true
		}
	}
	return false
}

// handleFsEvents 按事件提示刷新对应内容（主线程调用）。
func (ws *workspace) handleFsEvents(root string, paths []string) {
	if ws.mode != "work" || ws.root != root {
		return
	}
	hits := func(abs string) bool {
		for _, p := range paths {
			if hitHint(root, p, abs) {
				return true
			}
		}
		return false
	}
	// 打开中的 diff 或文件内容变化 → 重读（提示是目录级，是否真的变化由
	// 重读结果判定，内容相同不动已打开的会话）
	if ws.diffv.open != nil && hits(ws.diffv.target()) {
		ws.reloadDiff()
	}
	if ws.dirs.file != nil && hits(ws.dirs.file.abs) {
		ws.reloadOpenFile()
	}
	// 目录模式：受影响的已加载目录重读
	if ws.sidebarTab == "dir" {
		targets := []string{root}
		for abs := range ws.dirs.expanded {
			if _, cached := ws.dirs.cache[abs]; cached {
				targets = append(targets, abs)
			}
		}
		var affected []string
		for _, target := range targets {
			for _, p := range paths {
				// hitHint 双向匹配已覆盖「事件粗于/细于目标」两个方向
				if hitHint(root, p, target) {
					affected = append(affected, target)
					break
				}
			}
		}
		if len(affected) > 0 {
			ws.reloadDirs(affected)
		}
	}
	// 受影响仓库 → 后台重新采集（主线程不做 git 调用）
	scoped := map[string]bool{}
	for _, r := range ws.repos {
		if hits(r.Path) {
			scoped[r.Path] = true
		}
	}
	if len(scoped) == 0 {
		return
	}
	var repos []git.Repo
	for _, r := range ws.repos {
		if scoped[r.Path] {
			repos = append(repos, r)
		}
	}
	loadAsync(ws, func() git.CollectChangesResult { return git.CollectChangesChecked(repos) },
		func(r git.CollectChangesResult) {
			// 期间已切走（回选择态/换目录）时作废
			if ws.mode != "work" || ws.root != root {
				return
			}
			// 读取失败的仓库沿用上一版条目：从受影响集合里摘掉，合并视为
			// 未变化——否则瞬态失败会被当成「这个仓库没有改动了」
			for path := range r.Failed {
				delete(scoped, path)
			}
			if ws.mergeChanges(r.Changes, scoped) {
				ws.applyChangedModel()
			}
		})
}

// mergeChanges 用 fresh 替换受影响仓库的条目，其余仓库保持；返回是否有变
// 化。按「重读-比对-落地」：受影响仓库的改动段与当前一致（重读没读出新东
// 西）时整体跳过，调用方不必重建派生。比对只能按仓库分段做——合并会把
// scoped 段移到尾部，整表顺序必变，整表比对恒为不等。
func (ws *workspace) mergeChanges(fresh []git.Change, scoped map[string]bool) bool {
	old := map[string][]git.Change{}
	for _, c := range ws.changes {
		if scoped[c.RepoPath] {
			old[c.RepoPath] = append(old[c.RepoPath], c)
		}
	}
	freshByRepo := map[string][]git.Change{}
	for _, c := range fresh {
		freshByRepo[c.RepoPath] = append(freshByRepo[c.RepoPath], c)
	}
	unchanged := true
	for path := range scoped {
		if !slices.Equal(old[path], freshByRepo[path]) {
			unchanged = false
			break
		}
	}
	if unchanged {
		return false
	}
	out := make([]git.Change, 0, len(ws.changes)+len(fresh))
	for _, c := range ws.changes {
		if !scoped[c.RepoPath] {
			out = append(out, c)
		}
	}
	ws.changes = append(out, fresh...)
	return true
}

// structureChanged 对比仓库集合（rel 集合）是否变化。
func (ws *workspace) structureChanged(found []git.Repo) bool {
	if len(found) != len(ws.repos) {
		return true
	}
	old := map[string]bool{}
	for _, r := range ws.repos {
		old[r.Rel] = true
	}
	for _, r := range found {
		if !old[r.Rel] {
			return true
		}
	}
	return false
}

// watchErr 报告监听失败（应用仍可用，只是没有准实时刷新，手动刷新兜底）。
func (ws *workspace) watchErr(err error) {
	if ws.mode == "work" {
		ws.setErr("文件监听失败", err.Error())
	}
}
