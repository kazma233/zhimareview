package main

import "testing"

// 监听排除与仓库扫描共用 git.SkipDir 口径：点前缀目录（.idea/.git 等）
// 与依赖/构建产物不监听，普通业务路径不排除。
func TestWatchIgnorePath(t *testing.T) {
	cases := []struct {
		path   string
		ignore bool
	}{
		// 旧白名单漏排、编辑器高频写入的隐藏目录：必须排除
		{".idea/workspace.xml", true},
		{".svn/entries", true},
		// 嵌套仓库的 .git 不在首段：防「写 index → hint → 再采集」回环
		{"repo/.git/HEAD", true},
		// 依赖/构建产物
		{"node_modules/pkg/index.js", true},
		{"build/out.o", true},
		// 普通业务路径不排除
		{"src/main.go", false},
		{"main.go", false},
	}
	for _, c := range cases {
		if got := watchIgnorePath(c.path, true); got != c.ignore {
			t.Errorf("watchIgnorePath(%q) = %v, want %v", c.path, got, c.ignore)
		}
	}
}
