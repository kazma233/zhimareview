package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/store"
)

// 错误以弹窗呈现：标题与正文都在框内，复制按钮把完整错误写进剪贴板，
// 关闭按钮收起弹窗（内容留在 tab 上，切回来还能看到）。

func errApp(t *testing.T) (*application, *ui.Tester) {
	t.Helper()
	a := restoreApplication(&store.Session{Roots: []string{"/tmp/projA"}, Active: 0})
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	return a, tt
}

func TestErrorDialogShowsTitleAndBody(t *testing.T) {
	a, tt := errApp(t)
	ws := a.activeTab()
	ws.setErr("pull 失败", "error: Your local changes would be overwritten by merge:\n\tsrc/a.java\nAborting")

	tt.Frame()
	tt.Frame()

	if !tt.HasText("pull 失败") {
		t.Fatalf("dialog title missing: %q", tt.Texts())
	}
	if !tt.HasText("Aborting") {
		t.Fatalf("dialog body missing: %q", tt.Texts())
	}
	// 多行正文逐行渲染：第二行不与第一行合并
	if !tt.HasText("src/a.java") {
		t.Fatalf("dialog body line missing: %q", tt.Texts())
	}
	// 复制与关闭按钮
	if !tt.HasText("复制") || !tt.HasText("关闭") {
		t.Fatalf("dialog buttons missing: %q", tt.Texts())
	}
	// 内联错误条已移除：正文不在工作区顶栏下方的条里
	if ws.err == nil || ws.errOpen != true {
		t.Fatalf("err state = %+v, want open", ws.err)
	}
}

func TestErrorDialogCopyWritesClipboard(t *testing.T) {
	a, tt := errApp(t)
	ws := a.activeTab()
	ws.setErr("读取提交历史失败", "fatal: bad object HEAD")
	tt.Frame()
	tt.Frame()

	if err := tt.Click("复制"); err != nil {
		t.Fatalf("click copy: %v", err)
	}
	tt.Frame()
	tt.Frame()

	// 剪贴板里是完整错误（标题 + 正文）
	got := tt.Clipboard()
	if !strings.Contains(got, "读取提交历史失败") || !strings.Contains(got, "fatal: bad object HEAD") {
		t.Fatalf("clipboard = %q, want title and body", got)
	}
}

func TestErrorDialogCloseKeepsError(t *testing.T) {
	a, tt := errApp(t)
	ws := a.activeTab()
	ws.setErr("pull 失败", "Aborting")
	tt.Frame()
	tt.Frame()

	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("click close: %v", err)
	}
	tt.Frame()
	tt.Frame()

	if ws.errOpen {
		t.Fatalf("dialog still open after close")
	}
	if ws.err == nil {
		t.Fatalf("error content dropped on close; want kept for the tab")
	}
	// 关闭后正文不再占据界面
	if tt.HasText("Aborting") {
		t.Fatalf("dialog still rendered after close")
	}
}

// 成功路径清掉错误：弹窗不再出现。
func TestErrorDialogClearedOnSuccess(t *testing.T) {
	a, tt := errApp(t)
	ws := a.activeTab()
	ws.setErr("pull 失败", "Aborting")
	tt.Frame()
	tt.Frame()

	ws.clearErr()
	tt.Frame()
	tt.Frame()

	if tt.HasText("Aborting") {
		t.Fatalf("dialog still rendered after clear")
	}
	if ws.err != nil {
		t.Fatalf("err = %+v, want nil", ws.err)
	}
}

// 红点（未读错误标记）的消失时机：看过弹窗后消失，错误内容仍留在 tab 上。
func TestErrBadgeClearsAfterRead(t *testing.T) {
	a, tt := errApp(t)
	ws := a.activeTab()
	ws.setErr("pull 失败", "Aborting")
	tt.Frame()
	tt.Frame()

	if ws.errSeen {
		t.Fatalf("errSeen = true right after setErr, want unread")
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("click close: %v", err)
	}
	tt.Frame()
	tt.Frame()

	if !ws.errSeen {
		t.Fatalf("errSeen = false after close, want read")
	}
	if ws.err == nil {
		t.Fatalf("error content dropped, want kept on the tab")
	}
	// 红点消失：错误已读
	if ws.err != nil && !ws.errSeen {
		t.Fatalf("badge still unread after close")
	}
}

// 切走 tab 时关掉该 tab 的错误弹窗并标记已读：切回来不再弹。
func TestErrDialogClosesOnTabSwitch(t *testing.T) {
	a, tt := errApp(t)
	ws := a.activeTab()
	ws.setErr("pull 失败", "Aborting")
	tt.Frame()
	tt.Frame()

	// 切到另一个 tab
	a.newTab()
	tt.Frame()
	tt.Frame()

	if ws.errOpen {
		t.Fatalf("dialog still open after switching away")
	}
	if !ws.errSeen {
		t.Fatalf("errSeen = false after switching away, want read")
	}
	if ws.err == nil {
		t.Fatalf("error content dropped on switch, want kept")
	}
}
