package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"zhimareview/internal/git"
	"zhimareview/internal/store"
)

// 外部 git 操作（commit / add / stash）只改 .git 内部状态，被监听的路径
// 没有事件，界面不会自己更新——切 tab 与窗口获得焦点时补一次重扫。

// refreshOnDemand 的节流：连着触发只跑一次重扫。断言走语义副作用（refresh
// 必经 applyScanState 的 clearErr，用哨兵错误验证跑没跑），不做时间戳比较
// ——Windows 时钟粗粒度，同一刻度内的两次 time.Now() 挂钟与单调读数都相等。
func TestRefreshOnDemandThrottle(t *testing.T) {
	a := restoreApplication(&store.Session{Roots: []string{"/tmp/projA"}, Active: 0})
	ws := a.activeTab()

	// 刚扫过 → 跳过：哨兵错误不被清
	ws.lastRefresh = time.Now()
	ws.setErr("sentinel", "x")
	ws.refreshOnDemand()
	if ws.errText() == "" {
		t.Fatalf("refresh ran within throttle window")
	}

	// 超过节流间隔 → 重扫：哨兵错误被 clearErr 清掉
	ws.lastRefresh = time.Now().Add(-2 * refreshThrottle)
	ws.refreshOnDemand()
	if ws.errText() != "" {
		t.Fatalf("refresh did not run after throttle window")
	}
}

// pick 态 tab 不重扫（没有根目录可扫）。
func TestRefreshOnDemandSkipsPick(t *testing.T) {
	a := restoreApplication(nil)
	ws := a.activeTab()
	ws.refreshOnDemand()
	if !ws.lastRefresh.IsZero() {
		t.Fatalf("pick tab refreshed: lastRefresh = %v", ws.lastRefresh)
	}
}

// 切 tab 触发新激活 tab 的重扫。
func TestTabSwitchRefreshesNewTab(t *testing.T) {
	a := restoreApplication(&store.Session{Roots: []string{"/tmp/projA", "/tmp/projB"}, Active: 0})
	first, second := a.tabs[0], a.tabs[1]
	// 模拟「上次扫描很久之前」，切 tab 时应当触发重扫
	first.lastRefresh = time.Now().Add(-2 * refreshThrottle)
	second.lastRefresh = time.Now().Add(-2 * refreshThrottle)

	a.active = 1
	a.tabChanged()

	if second.lastRefresh.Before(time.Now().Add(-refreshThrottle)) {
		t.Fatalf("newly active tab was not refreshed")
	}
	if !first.lastRefresh.Equal(time.Now().Add(-2*refreshThrottle).Truncate(time.Millisecond)) &&
		first.lastRefresh.After(time.Now().Add(-refreshThrottle)) {
		t.Fatalf("inactive tab was refreshed")
	}
}

// 扫描落地比对：仓库与改动都和当前一致（例行重扫没扫出新东西）时不动
// 派生，有变化才重建——实抓：重扫走了整体替换，派生索引与侧栏行整表重建。
func TestApplyScanStateSkipsUnchangedRebuild(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	p1 := ws.changeByAbs[root+"/a.txt"]

	// 磁盘未动，原样重扫：派生不重建（重建会把索引指向新切片元素）。
	// 纯内容修改不算变化——比对对象是改动列表（git 状态），内容级修改
	// 产生的列表逐值相同，正确地被跳过
	ws.applyScanState(root, git.FindRepos(root), git.CollectChanges(repos))
	if ws.changeByAbs[root+"/a.txt"] != p1 {
		t.Fatal("derived index rebuilt on unchanged scan")
	}

	// 改动列表真变了（未跟踪 → 暂存新增）：重建并反映新状态
	gitCmd(t, root, "add", "a.txt")
	ws.applyScanState(root, git.FindRepos(root), git.CollectChanges(repos))
	if ws.changeByAbs[root+"/a.txt"] == p1 {
		t.Fatal("derived index not rebuilt on changed scan")
	}
	if len(ws.changes) != 1 || ws.changes[0].Code != "A" {
		t.Errorf("changes = %+v, want staged a.txt", ws.changes)
	}
}
