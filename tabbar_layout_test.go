package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 标题栏区布局回归：模拟 TitleBarHidden 下系统报告的红绿灯占位
// （macOS 标准标题栏高 ~28、红绿灯右缘 ~80、灯垂直居中于该带），断言
// tab 条与灯对齐、内容区层次与留白——防「tab 低于红绿灯」「tab 压灯」
// 类真机问题复发。
func TestTabBarLayoutWithNativeTitleBar(t *testing.T) {
	a := restoreApplication(testSession(0, "/tmp/projA", "/tmp/sandbox"))
	tt := ui.NewTester(a.view, 930, 600)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 80, Bottom: 28})
	tt.Frame()
	tt.Frame()

	const barH = float32(28) // 行高跟 bar.Height（对齐红绿灯），兜底不触发

	// 内容区从 tab 条之下开始：顶栏路径的 Y 不得侵入标题栏区
	r, ok := tt.Find("/tmp/projA")
	if !ok {
		t.Fatalf("topbar path not found")
	}
	if r.Y < barH-1 {
		t.Fatalf("topbar Y = %.1f, want >= %.0f (below tab bar)", r.Y, barH)
	}

	// tab 不紧贴红绿灯：起点越过系统占位再留一道间隙
	tab, ok := tt.Find("projA")
	if !ok {
		t.Fatalf("tab label not found")
	}
	if tab.X < 80+tabGap {
		t.Fatalf("tab X = %.1f, want >= %.0f (traffic lights + gap)", tab.X, 80+tabGap)
	}
	// tab 与红绿灯垂直居中对齐：灯中心在带高一半，tab 行内居中应一致
	if center := tab.Y + tab.H/2; center < barH/2-1 || center > barH/2+1 {
		t.Fatalf("tab center Y = %.1f, want ~%.1f (aligned with traffic lights)", center, barH/2)
	}
	plus, ok := tt.Find("新建标签页")
	if !ok {
		t.Fatalf("plus button not found")
	}
	if plus.X <= tab.X {
		t.Fatalf("plus X = %.1f not after tab X = %.1f", plus.X, tab.X)
	}
	// ＋ 保持在窗口宽度之内（tab 多时也不许被挤出条外）
	if plus.X+plus.W > 930 {
		t.Fatalf("plus right edge %.1f overflows window 930", plus.X+plus.W)
	}
}

// tab 多到超出窗口宽度：滚动区收纳内容，＋ 固定在右端不被挤出窗口。
// 根目录唯一（/tmp/projects 的 N 个子目录），避免误配「标签页 X」精确名。
func TestTabBarOverflowScrolls(t *testing.T) {
	var roots []string
	for i := 0; i < 20; i++ {
		roots = append(roots, fmt.Sprintf("/tmp/scrollcase-%02d", i))
	}
	a := restoreApplication(testSession(0, roots...))
	tt := ui.NewTester(a.view, 930, 600)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 80, Bottom: 28})
	tt.Frame()
	tt.Frame()

	plus, ok := tt.Find("新建标签页")
	if !ok {
		t.Fatalf("plus button not found")
	}
	if plus.X+plus.W > 930 {
		t.Fatalf("plus right edge %.1f overflows window 930 with 20 tabs", plus.X+plus.W)
	}
	// 滚动偏移为 0：首个 tab 仍在视口起点附近（红绿灯留白 + 左箭头之后）
	first, ok := tt.Find("scrollcase-00")
	if !ok {
		t.Fatalf("first tab not found; texts=%q", tt.Texts())
	}
	if first.X > 200 {
		t.Fatalf("first tab X = %.1f, want near the left edge", first.X)
	}
	// 溢出时左右箭头在场
	for _, s := range []string{"向左滚动标签页", "向右滚动标签页"} {
		if _, ok := tt.Find(s); !ok {
			t.Fatalf("%s not shown on overflow", s)
		}
	}
}

// panic 回归（实抓）：多 tab 下渲染中途点 ×，同帧 range 循环按旧长度迭代
// 截短后的切片会越界；修复后点击只入队、tabBar 渲染完统一 flush。
func TestTabBarCloseMidFrameNoPanic(t *testing.T) {
	var roots []string
	for i := 0; i < 12; i++ {
		roots = append(roots, fmt.Sprintf("/tmp/closecase-%02d", i))
	}
	a := restoreApplication(testSession(5, roots...))
	tt := ui.NewTester(a.view, 930, 600)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 80, Bottom: 28})
	tt.Frame()
	tt.Frame()

	if err := tt.Click("关闭标签页 closecase-05"); err != nil {
		t.Fatalf("click close: %v", err)
	}
	tt.Frame()
	tt.Frame()

	if len(a.tabs) != 11 {
		t.Fatalf("tabs = %d, want 11", len(a.tabs))
	}
	for _, ws := range a.tabs {
		if ws.root == "/tmp/closecase-05" {
			t.Fatalf("closecase-05 still present")
		}
	}
	if a.active < 0 || a.active >= len(a.tabs) {
		t.Fatalf("active %d out of range after close", a.active)
	}
}
