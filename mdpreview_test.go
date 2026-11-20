package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Markdown 渲染模式切换用开关（Segmented）而非改文案的按钮：两个选项
// 固定，按钮宽度与位置不随状态跳变。

func mdPreviewWorkspace(t *testing.T) (*workspace, *ui.Tester) {
	t.Helper()
	root := filepath.ToSlash(t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# 标题\n\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.openFile(filepath.Join(root, "a.md"))
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	return ws, tt
}

func TestMarkdownPreviewSwitch(t *testing.T) {
	ws, tt := mdPreviewWorkspace(t)

	// 开关的两个选项都在，且是常驻文案
	if !tt.HasText("预览") || !tt.HasText("源码") {
		t.Fatalf("preview switch options missing: %q", tt.Texts())
	}
	if ws.mdPreview {
		t.Fatalf("mdPreview = true, want source view by default")
	}

	// 切到预览：ws.mdPreview 翻转
	if err := tt.Click("预览"); err != nil {
		t.Fatalf("click preview: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !ws.mdPreview {
		t.Fatalf("mdPreview = false after clicking 预览")
	}

	// 切回源码
	if err := tt.Click("源码"); err != nil {
		t.Fatalf("click source: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if ws.mdPreview {
		t.Fatalf("mdPreview = true after clicking 源码")
	}
}

// 预览模式不记忆：切到预览后打开另一个文件，回到源码视图（实抓：上一个
// 文件的预览态被带到了下一个）。
func TestMarkdownPreviewResetsOnOpen(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# 标题\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.openFile(filepath.Join(root, "a.md"))
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()

	// 切到预览
	if err := tt.Click("预览"); err != nil {
		t.Fatalf("click preview: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !ws.mdPreview {
		t.Fatalf("mdPreview = false after clicking 预览")
	}

	// 打开另一个文件：预览态不继承
	ws.openFile(filepath.Join(root, "b.md"))
	tt.Frame()
	tt.Frame()
	if ws.mdPreview {
		t.Fatalf("mdPreview = true after opening another file, want source")
	}
}
