package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRememberRecentTopAndDedupe(t *testing.T) {
	dir := t.TempDir()

	RememberRecent(dir, "D:/a")
	RememberRecent(dir, "D:/b")
	got := RememberRecent(dir, "D:/a")

	if got[0] != "D:/a" {
		t.Errorf("recent = %v, want D:/a first", got)
	}
	if len(got) != 2 {
		t.Errorf("recent = %v, want 2 entries", got)
	}
	if loaded := LoadRecent(dir); len(loaded) != 2 || loaded[0] != "D:/a" {
		t.Errorf("loaded = %v, want D:/a first after reload", loaded)
	}
}

func TestLoadRecentMissingFile(t *testing.T) {
	if got := LoadRecent(t.TempDir()); got != nil {
		t.Errorf("LoadRecent(missing) = %v, want nil", got)
	}
}

func TestLoadRecentCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "recent.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadRecent(dir); got != nil {
		t.Errorf("LoadRecent(corrupt) = %v, want nil", got)
	}
}

func TestRememberRecentCapFifty(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 60; i++ {
		RememberRecent(dir, fmt.Sprintf("D:/repos-%02d", i))
	}
	got := LoadRecent(dir)
	if len(got) != 50 {
		t.Errorf("recent = %d entries, want capped at 50", len(got))
	}
	if got[0] != "D:/repos-59" {
		t.Errorf("recent[0] = %q, want latest first", got[0])
	}
}

func TestSessionRoundtrip(t *testing.T) {
	dir := t.TempDir()
	if got := LoadSession(dir); got != nil {
		t.Fatalf("LoadSession(missing) = %v, want nil", got)
	}
	want := Session{Tabs: []Tab{
		{Root: "/a"},
		{Root: "/b", Name: "前端", Color: "red"},
	}, Active: 1}
	if err := SaveSession(dir, want); err != nil {
		t.Fatal(err)
	}
	got := LoadSession(dir)
	if got == nil || len(got.Tabs) != 2 || got.Tabs[1] != want.Tabs[1] || got.Active != 1 {
		t.Fatalf("LoadSession() = %+v, want %+v", got, want)
	}
}

// 旧版 session.json 只有 roots 字符串；读取时转换为带空元数据的 Tab。
func TestLoadSessionLegacyRoots(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"roots":["/a","/b"],"active":1}`)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadSession(dir)
	want := []Tab{{Root: "/a"}, {Root: "/b"}}
	if got == nil || len(got.Tabs) != 2 || got.Tabs[0] != want[0] || got.Tabs[1] != want[1] || got.Active != 1 {
		t.Fatalf("LoadSession(legacy) = %+v, want tabs %+v active 1", got, want)
	}
}

func TestLoadSessionCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadSession(dir); got != nil {
		t.Errorf("LoadSession(corrupt) = %v, want nil", got)
	}
}

func TestSaveSessionEmptyOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSession(dir, Session{Tabs: []Tab{{Root: "/a"}}, Active: 0}); err != nil {
		t.Fatal(err)
	}
	// 全 pick 关闭：空会话必须覆盖旧记录，否则旧 tab 在下次启动复活
	if err := SaveSession(dir, Session{Tabs: nil, Active: -1}); err != nil {
		t.Fatal(err)
	}
	got := LoadSession(dir)
	if got == nil || len(got.Tabs) != 0 {
		t.Fatalf("LoadSession() = %+v, want empty tabs", got)
	}
}

func TestLoadSessionFiltersEmptyRoots(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"tabs":[{"root":"/a"},{"root":""},{"root":"/b"}],"active":2}`)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadSession(dir)
	if got == nil || len(got.Tabs) != 2 || got.Active != 2 {
		t.Fatalf("LoadSession() = %+v, want 2 tabs with active 2", got)
	}
}

func TestConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()
	// 缺失文件 → 默认值（统一、折叠、不勾选）
	if got := LoadConfig(dir); got.Diff != (DiffPrefs{}) {
		t.Fatalf("LoadConfig(missing) = %+v, want defaults", got.Diff)
	}
	want := Config{Diff: DiffPrefs{Layout: 1, Expand: true, ShowDiff: true}}
	if err := SaveConfig(dir, want); err != nil {
		t.Fatal(err)
	}
	if got := LoadConfig(dir); got.Diff != want.Diff {
		t.Fatalf("LoadConfig() = %+v, want %+v", got.Diff, want.Diff)
	}
}

func TestLoadConfigCorruptAndInvalid(t *testing.T) {
	dir := t.TempDir()
	// 损坏 → 默认值
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadConfig(dir); got.Diff != (DiffPrefs{}) {
		t.Errorf("LoadConfig(corrupt) = %+v, want defaults", got.Diff)
	}
	// 非法 layout 收敛到统一
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"diff":{"layout":7,"expand":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadConfig(dir)
	if got.Diff.Layout != 0 || !got.Diff.Expand {
		t.Errorf("LoadConfig(invalid layout) = %+v, want layout 0 expand true", got.Diff)
	}
}
