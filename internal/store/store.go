// Package store 管理跨窗口共享的最近根目录列表与单窗口的会话（打开的项目 tab）。
// 存储损坏不构成致命错误：调用方静默降级为空列表，最近列表只是入口的便利功能。
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// 入口页最多展示 50 条最近目录：列表区可滚动，上限放宽到覆盖重度使用。
const maxRecent = 50

// LoadRecent 返回最近使用的根目录，最近一次的在前。
// 文件不存在或内容损坏时返回 nil 而非错误：列表缺失不影响选择目录进入。
func LoadRecent(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "recent.json"))
	if err != nil {
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// RememberRecent 把 root 置顶并持久化，返回新的列表。
// 写盘失败时仍返回内存中的新列表：本次会话可用，下次启动可能丢一条记录。
func RememberRecent(dir, root string) []string {
	out := []string{root}
	for _, item := range LoadRecent(dir) {
		if item != root && len(out) < maxRecent {
			out = append(out, item)
		}
	}
	if data, err := json.Marshal(out); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "recent.json"), data, 0o644); err != nil && !errors.Is(err, os.ErrNotExist) {
			return out
		}
	}
	return out
}

// Tab 是会话里一个项目 tab：根目录 + 用户自定义的展示名与颜色键。
// 名称/颜色为空表示按根目录自动命名、不设色；tab 内部的查看状态不恢复。
type Tab struct {
	Root  string `json:"root"`
	Name  string `json:"name,omitempty"`
	Color string `json:"color,omitempty"`
}

// Session 是一次关闭时的窗口会话：打开了哪些项目 tab（按 tab 顺序）与
// 激活下标。
type Session struct {
	Tabs   []Tab `json:"tabs,omitempty"`
	Active int   `json:"active"`
}

// sessionFile 是 session.json 的磁盘形状。roots 是 tab 元数据加入前的
// 旧格式（只有根目录字符串），读取一次转换为 Tabs，写入只走新格式。
type sessionFile struct {
	Tabs   []Tab    `json:"tabs"`
	Roots  []string `json:"roots"`
	Active int      `json:"active"`
}

// LoadSession 读取上次会话；文件不存在或损坏返回 nil（按首次启动处理）。
func LoadSession(dir string) *Session {
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil
	}
	var f sessionFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	tabs := f.Tabs
	if len(tabs) == 0 {
		tabs = make([]Tab, len(f.Roots))
		for i, root := range f.Roots {
			tabs[i].Root = root
		}
	}
	out := make([]Tab, 0, len(tabs))
	for _, tab := range tabs {
		if tab.Root != "" {
			out = append(out, tab)
		}
	}
	return &Session{Tabs: out, Active: f.Active}
}

// SaveSession 持久化会话。空 Tabs 也是合法状态（上次关闭时没有打开的
// 项目），必须落盘覆盖旧记录——不写会让更早的会话在下次启动复活。
func SaveSession(dir string, s Session) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "session.json"), data, 0o644)
}
