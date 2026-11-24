package main

// 侧栏树行几何与展开行为回归：计数列固定右缘（不随深度左移）、
// 改动树默认全展开（收起只记用户显式收起的那层）、目录树默认全收起。

import (
	"fmt"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// 文件夹开/合图标必须真的画出来，且两态形状不同（矢量自绘无文本锚点，
// 用帧像素断言防 Draw 退化）。
func TestSidebarFolderIconPainted(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	repos := []git.Repo{
		{Path: "D:/repos/a", Name: "aclosed", Rel: "aclosed"},
		{Path: "D:/repos/b", Name: "bopen", Rel: "bopen"},
	}
	changes := []git.Change{
		{RepoPath: "D:/repos/a", Path: "f1.java", Section: "unstaged", Code: "M"},
		{RepoPath: "D:/repos/b", Path: "f2.java", Section: "unstaged", Code: "M"},
	}
	ws.applyScanState("D:/repos", repos, changes)
	// 默认全展开；显式收起第一行，得到开/合两态
	ws.toggleChange(repoID("unstaged", "D:/repos/a"))

	tt := ui.NewTester(ws.view, 300, 800)
	tt.Frame()
	img := tt.Image()

	// 分组后顶部多一行「未暂存」组标题（行高 24）；depth0 两行依次下移：
	// 图标盒在行首 x 4..18，行高 24 内垂直居中 y+5..y+19
	iconPixels := func(rowY int) map[uint32]int {
		set := map[uint32]int{}
		for py := rowY + 5; py < rowY+19; py++ {
			for px := 4; px < 18; px++ {
				c := img.RGBAAt(px, py)
				set[uint32(c.R)<<16|uint32(c.G)<<8|uint32(c.B)]++
			}
		}
		return set
	}
	closed := iconPixels(106)
	open := iconPixels(130)
	if len(closed) < 3 || len(open) < 3 {
		t.Errorf("icon not painted: closed colors=%d open colors=%d", len(closed), len(open))
	}
	same := len(closed) == len(open)
	if same {
		for k, v := range closed {
			if open[k] != v {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("open/closed icons render identically, want distinct shapes")
	}
}

// 计数列必须固定贴行右缘，不随深度左移成锯齿（Outline 时代的对齐契约）；
// 列的绝对位置含滚动条避让带（行右 padding 12，见 sideRowView）。
func TestSidebarCountColumnAligned(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	repos := []git.Repo{{Path: "D:/repos/t", Name: "repo", Rel: ""}}
	// 九层链，第 i 层挂 i+1 个文件，聚合 count 逐层唯一
	var changes []git.Change
	prefix := ""
	for i := 1; i <= 9; i++ {
		prefix += fmt.Sprintf("c%d/", i)
		for j := range i + 1 {
			changes = append(changes, git.Change{
				RepoPath: "D:/repos/t",
				Path:     fmt.Sprintf("%sf%d_%d.java", prefix, i, j),
				Section:  "unstaged", Code: "M",
			})
		}
	}
	ws.applyScanState("D:/repos", repos, changes)

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	for _, info := range tt.AccessAll() {
		switch info.Name {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "10":
			if b := info.Bounds; b.W > 0 && b.W < 300 && b.X < 300 {
				if right := b.X + b.W; right < 282 {
					t.Errorf("count %q right edge = %.1f, want fixed at ~288 (not staircasing with depth)", info.Name, right)
				}
			}
		}
	}
}

// 改动树默认全展开：所有 branch 行 open、文件行直接可见；收起只收该层
// 子树且跨扫描保持，其余目录不受影响；再展开回到缺省。
func TestChangesTreeDefaultExpanded(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	repos := []git.Repo{{Path: "D:/repos/t", Name: "repo", Rel: ""}}
	changes := []git.Change{
		{RepoPath: "D:/repos/t", Path: "chain/a1/a2/deep.java", Section: "unstaged", Code: "M"},
		{RepoPath: "D:/repos/t", Path: "flat/direct.java", Section: "unstaged", Code: "M"},
	}
	ws.applyScanState("D:/repos", repos, changes)

	rows := map[string]sideRow{}
	for _, r := range ws.tree.rows {
		rows[r.id] = r
	}
	// 全展开：无文件的单链目录合并为一行 chain/a1/a2（label 路径、id 取最深层），
	// 行 open，文件行直接入行
	chain, ok := rows[dirID("unstaged", "chain/a1/a2")]
	if !ok {
		t.Fatalf("compressed chain row missing: %v rows", len(ws.tree.rows))
	}
	if chain.label != "chain/a1/a2" {
		t.Errorf("chain label = %q, want compressed path", chain.label)
	}
	if !chain.open {
		t.Error("compressed chain not expanded by default")
	}
	if r := rows[dirID("unstaged", "flat")]; !r.open {
		t.Error("flat dir not expanded by default")
	}
	if _, ok := rows["cf:D:/repos/t|chain/a1/a2/deep.java|unstaged"]; !ok {
		t.Error("deep file row missing under default-expanded tree")
	}

	// 收起合并行：其子树消失，其余目录保持展开
	ws.toggleChange(dirID("unstaged", "chain/a1/a2"))
	rows = map[string]sideRow{}
	for _, r := range ws.tree.rows {
		rows[r.id] = r
	}
	if r := rows[dirID("unstaged", "chain/a1/a2")]; r.open {
		t.Error("collapsed chain still open")
	}
	for _, gone := range []string{"cf:D:/repos/t|chain/a1/a2/deep.java|unstaged"} {
		if _, ok := rows[gone]; ok {
			t.Errorf("row %q should disappear under collapsed chain", gone)
		}
	}
	if r := rows[dirID("unstaged", "flat")]; !r.open {
		t.Error("unrelated dir must stay expanded")
	}

	// 重新扫描（同 root）：显式收起保持，其余仍全展开
	ws.applyScanState("D:/repos", repos, changes)
	rows = map[string]sideRow{}
	for _, r := range ws.tree.rows {
		rows[r.id] = r
	}
	if r := rows[dirID("unstaged", "chain/a1/a2")]; r.open {
		t.Error("explicit collapse must survive rescan")
	}
	if _, ok := rows["cf:D:/repos/t|flat/direct.java|unstaged"]; !ok {
		t.Error("other branches must stay expanded after rescan")
	}

	// 再点合并行：回到缺省展开
	ws.toggleChange(dirID("unstaged", "chain/a1/a2"))
	rows = map[string]sideRow{}
	for _, r := range ws.tree.rows {
		rows[r.id] = r
	}
	if r := rows[dirID("unstaged", "chain/a1/a2")]; !r.open {
		t.Error("re-toggle must restore default expanded")
	}
}

// 目录树（目录列表 tab）默认全收起：未展开时只有根层行。
func TestDirTreeDefaultCollapsed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/sub/inner", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/loose.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = dir
	ws.applyScanState(dir, nil, nil)
	ws.sidebarTab = "dir"
	ws.loadDir(dir)

	// 根层两行（sub + loose.txt），sub 未展开时 inner 不入行
	if len(ws.dirs.rows) != 2 {
		t.Fatalf("rows = %d, want 2 (collapsed root level)", len(ws.dirs.rows))
	}
	for _, r := range ws.dirs.rows {
		if r.branch && r.open {
			t.Errorf("dir row %q expanded by default, want collapsed", r.id)
		}
	}
}
