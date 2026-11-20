package main

import (
	"testing"
	"time"

	"zhimareview/internal/store"
)

// 外部 git 操作（commit / add / stash）只改 .git 内部状态，被监听的路径
// 没有事件，界面不会自己更新——切 tab 与窗口获得焦点时补一次重扫。

// refreshOnDemand 的节流：连着触发只跑一次重扫。
func TestRefreshOnDemandThrottle(t *testing.T) {
	a := restoreApplication(&store.Session{Roots: []string{"/tmp/projA"}, Active: 0})
	ws := a.activeTab()

	// 刚扫过 → 跳过
	ws.lastRefresh = time.Now()
	before := ws.lastRefresh
	ws.refreshOnDemand()
	if !ws.lastRefresh.Equal(before) {
		t.Fatalf("refresh ran within throttle window")
	}

	// 超过节流间隔 → 重扫并记下时间
	ws.lastRefresh = time.Now().Add(-2 * refreshThrottle)
	ws.refreshOnDemand()
	if ws.lastRefresh.Equal(before) {
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
