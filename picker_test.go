package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 选择态：入口文案、选目录按钮、最近目录列表。
func TestPickerView(t *testing.T) {
	ws := newWorkspace(nil)
	ws.recent = []string{"D:/repos", "D:/work"}
	tt := ui.NewTester(ws.view, 1180, 800)

	if !tt.HasText("选择目录…") {
		t.Errorf("texts = %q, want 选择目录… button", tt.Texts())
	}
	if !tt.HasText("D:/repos") || !tt.HasText("D:/work") {
		t.Errorf("texts = %q, want recent dirs listed", tt.Texts())
	}

	// 点击最近目录进入工作区：顶栏显示根路径
	if err := tt.Click("D:/repos"); err != nil {
		t.Fatal(err)
	}
	if ws.mode != "work" || ws.root != "D:/repos" {
		t.Errorf("mode = %q root = %q, want work / D:/repos", ws.mode, ws.root)
	}
	if !tt.HasText("D:/repos") {
		t.Errorf("texts = %q, want root in topbar", tt.Texts())
	}
}

// 最近目录条目数上限由 store 层保证；这里验证空数据不渲染区块。
func TestPickerWithoutRecent(t *testing.T) {
	ws := newWorkspace(nil)
	tt := ui.NewTester(ws.view, 1180, 800)

	if tt.HasText("最近使用") {
		t.Errorf("texts = %q, want no recent section", tt.Texts())
	}
}
