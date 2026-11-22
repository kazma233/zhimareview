package main

// 历史页溢出回归：页面根列与内层 Split 曾用 Fill（父容器高度 100%），叠加
// 顶栏/下拉行后列底越出窗口——提交列表滚到底后最后几条真机不可达。
// 断言：滚到底后靠后的提交行完整可见且在窗口底边之上。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

func TestHistoryPageStaysInWindow(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.activeRepo = "D:/repos/demo"
	ws.hist.all = make([]git.CommitRow, 50)
	for i := range ws.hist.all {
		ws.hist.all[i] = git.CommitRow{
			Hash:      fmt.Sprintf("h%02d0123456789", i),
			ShortHash: fmt.Sprintf("h%02d", i),
			Subject:   fmt.Sprintf("commit %02d", i),
		}
	}
	ws.hist.commits = ws.hist.all
	ws.hist.done = true

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if _, ok := tt.Find("h00"); !ok {
		t.Fatalf("first commit not visible: %q", tt.Texts())
	}

	// 滚到底：末行必须完整落在窗口底边之上
	for range 8 {
		tt.Scroll(200, 400, 0, 600)
		tt.Advance(300 * time.Millisecond)
	}
	r, ok := tt.Find("h49")
	if !ok || r.H <= 0 || r.Y+r.H > 800 {
		t.Fatalf("last commit after scroll-to-bottom = %+v ok=%v, want fully inside 800-high window", r, ok)
	}
}

// 提交历史页布局契约：
// 1. 仓库切换下拉只在左列宽度内，不占整页顶部；右列 diff 从页面顶端占满高度；
// 2. 引用徽标（分支）在 commit message 右侧；
// 3. 固定表头悬在列表上方、列与行内容对齐。
func TestHistoryPageLayout(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.hist.sel = 0
	ws.activeRepo = "D:/repos/group/ra"
	ws.hist.repoCombo = "group/ra"
	ws.hist.repoOptions = []string{"group/ra", "group/rb"}
	ws.hist.repoByLabel = map[string]string{"group/ra": "D:/repos/group/ra", "group/rb": "D:/repos/group/rb"}
	ws.hist.all = []git.CommitRow{{
		Hash:      "abc123def456",
		ShortHash: "abc123d",
		Author:    "alice",
		Date:      "2026-11-16",
		Refs:      "origin/develop",
		Subject:   "fix: sample subject",
	}}
	ws.hist.commits = ws.hist.all
	ws.hist.done = true

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()

	// 下拉在左列宽度内（historySplit=420）：Combobox 的值在 a11y 里是
	// Value 不是 Name，按角色扫
	var cb ui.Rect
	cbOK := false
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleComboBox {
			cb, cbOK = info.Bounds, true
			break
		}
	}
	if !cbOK || cb.X+cb.W > 440 {
		t.Fatalf("repo switcher rect=%+v ok=%v, want inside left pane (≤~420)", cb, cbOK)
	}
	// 右列 revert 按钮贴近页面顶端（顶栏 44 之下），diff 占满高度
	rv, ok := tt.Find("revert")
	if !ok || rv.Y > 44+32 {
		t.Fatalf("revert button y=%.1f, want right pane starting at page top (≤76)", rv.Y)
	}
	// 固定表头：在远程操作行之下、列表首行之上
	hd, ok := tt.Find("提交信息")
	fb, okFetch := tt.Find("fetch")
	hr, okRow := tt.Find("abc123d")
	if !ok || !okFetch || !okRow || hd.Y < fb.Y+fb.H || hd.Y > hr.Y+2 {
		t.Fatalf("header y=%.1f (fetch bottom %.1f, first row y=%.1f), want between actions and list",
			hd.Y, fb.Y+fb.H, hr.Y)
	}
	// 表头列与行内容对齐：表头「提交」与行短哈希同列
	hh, _ := tt.Find("提交")
	if hh.X < 0 || hr.X < 0 {
		t.Fatalf("header/row not found: %+v %+v", hh, hr)
	}
	if d := hh.X - hr.X; d < -3 || d > 3 {
		t.Errorf("hash column misaligned: header x=%.1f row x=%.1f", hh.X, hr.X)
	}
	// 分支徽标在 message 右侧
	sub, _ := tt.Find("fix: sample subject")
	ref, ok := tt.Find("origin/develop")
	if !ok || ref.X < sub.X+sub.W {
		t.Fatalf("refs badge x=%.1f, want right of subject (ends %.1f)", ref.X, sub.X+sub.W)
	}
}

// 信息列（message+分支合并）契约：占满哈希与行尾作者/日期之间的剩余宽度、
// 超出省略号不换行；作者/日期钉在行尾，拖宽分隔条行尾空白不增长，表头与行
// 同列对齐；hover 0.6s 出提示层，完整展示 message 与全部分支（TooltipBase 原生）。
func TestCommitRowColumnsAndTooltip(t *testing.T) {
	long := "fix: " + strings.Repeat("segment-", 40) + "end"
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.hist.sel = 0
	ws.activeRepo = "D:/repos/group/ra"
	ws.hist.repoCombo = "group/ra"
	ws.hist.repoOptions = []string{"group/ra"}
	ws.hist.repoByLabel = map[string]string{"group/ra": "D:/repos/group/ra"}
	ws.hist.all = []git.CommitRow{{
		Hash:      "abc123def456",
		ShortHash: "abc123d",
		Author:    "alice",
		Date:      "2026-11-16",
		Refs:      "origin/a,origin/b,origin/c",
		Subject:   long,
	}}
	ws.hist.commits = ws.hist.all
	ws.hist.done = true

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()

	// 悬停前没有提示层
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleTooltip {
			t.Fatal("tooltip visible before hover")
		}
	}

	// 移到首行信息列上（行位置按实际渲染 rect 取，新增操作行不失效），等 600ms 延迟
	row, ok := tt.Find("abc123d")
	if !ok {
		t.Fatal("first commit row not found")
	}
	tt.Move(150, row.Y+row.H/2)
	tt.Advance(700 * time.Millisecond)
	tt.Frame()

	// 提示层出现：内容含完整 message 与全部分支（origin/c 只在提示层）
	tipFound, tipText := false, ""
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleTooltip && strings.Contains(info.Name, long) {
			tipFound, tipText = true, info.Name
			break
		}
	}
	if !tipFound {
		t.Fatal("no tooltip with full message after hover")
	}
	if !strings.Contains(tipText, "origin/c") {
		t.Errorf("tooltip missing third ref: %q", tipText)
	}

	// 作者/日期钉在行尾：日期右缘只留行 padding（信息列占满剩余宽度），
	// 拖宽分隔条行尾空白不增长；表头日期列与行日期列对齐
	pinned := func(tag string) {
		date, ok := tt.Find("2026-11-16")
		if !ok {
			t.Fatalf("%s: row date not found", tag)
		}
		// 列宽回归：日期/作者列放宽后不得换行（换行会撑高行内文本）
		if date.H > 16 {
			t.Errorf("%s: date wrapped: %+v", tag, date)
		}
		if date.W < commitDateW-1 {
			t.Errorf("%s: date width = %.1f, want at least %d", tag, date.W, commitDateW)
		}
		auth, ok := tt.Find("alice")
		if !ok {
			t.Fatalf("%s: row author not found", tag)
		}
		if auth.H > 16 {
			t.Errorf("%s: author wrapped: %+v", tag, auth)
		}
		if auth.W < commitAuthorW-1 {
			t.Errorf("%s: author width = %.1f, want at least %d", tag, auth.W, commitAuthorW)
		}
		if blank := float32(ws.hist.split) - (date.X + date.W); blank > 20 {
			t.Errorf("%s: blank right of date = %.0f at split %v, want pinned to pane edge", tag, blank, ws.hist.split)
		}
		hd, ok := tt.Find("日期")
		if !ok {
			t.Fatalf("%s: header date not found", tag)
		}
		if d := hd.X - date.X; d < -3 || d > 3 {
			t.Errorf("%s: header/row date column misaligned: header x=%.1f row x=%.1f", tag, hd.X, date.X)
		}
	}
	pinned("split=420")
	ws.hist.split = 900
	tt.Frame()
	pinned("split=900")
}

// 勾选「隐藏 merge」：列表过滤掉合并提交，勾选重置选中；再勾选恢复全量。
func TestHistoryHideMergesToggle(t *testing.T) {
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = "D:/repos"
	ws.mainTab = 1
	ws.activeRepo = "D:/repos/group/ra"
	ws.hist.repoOptions = []string{"group/ra"}
	ws.hist.all = []git.CommitRow{
		{Hash: "h1", ShortHash: "h1", Author: "a", Date: "2026-11-16", Subject: "normal one"},
		{Hash: "h2", ShortHash: "h2", Author: "a", Date: "2026-11-16", Subject: "Merge branch 'x'", Merge: true},
		{Hash: "h3", ShortHash: "h3", Author: "b", Date: "2026-11-15", Subject: "normal two"},
	}
	ws.refilterCommits()

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if len(ws.hist.commits) != 3 {
		t.Fatalf("commits = %d, want 3 before toggle", len(ws.hist.commits))
	}

	// 勾选「隐藏 merge」：合并提交从列表消失，选中重置
	if err := tt.Click("隐藏 merge"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(ws.hist.commits) != 2 || ws.hist.hideMerges != true {
		t.Fatalf("after toggle: commits=%d hideMerges=%v, want 2 / true", len(ws.hist.commits), ws.hist.hideMerges)
	}
	if tt.HasText("Merge branch 'x'") {
		t.Error("merge commit still listed after toggle")
	}
	if ws.hist.sel != -1 {
		t.Errorf("commitSel = %d, want reset to -1", ws.hist.sel)
	}

	// 再点：恢复全量
	if err := tt.Click("隐藏 merge"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(ws.hist.commits) != 3 || tt.HasText("Merge branch 'x'") == false {
		t.Fatalf("untoggle did not restore merges: commits=%d", len(ws.hist.commits))
	}
}

// 分页加载：首页 100 条；滚到接近末尾自动追加下一页；不足一页标记加载完毕。
// 夹具仓库 102 个空提交（>100 触发第二页）。
func TestCommitsPagination(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	for i := range 102 {
		cmd := exec.Command("git", "commit", "--allow-empty", "-m", fmt.Sprintf("c%03d", i))
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("empty commit %d: %v\n%s", i, err, out)
		}
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.mainTab = 1
	ws.activeRepo = root
	ws.loadCommits()
	if len(ws.hist.all) != 100 || ws.hist.done {
		t.Fatalf("first page = %d done=%v, want 100 / false", len(ws.hist.all), ws.hist.done)
	}

	// 渲染首屏：虚拟化列表只构建可见行；滚动接近末尾后末行被构建，触发追加
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if len(ws.hist.commits) != 100 {
		t.Fatalf("visible commits on first screen = %d, want 100", len(ws.hist.commits))
	}
	for range 5 {
		tt.Scroll(200, 400, 0, 600)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
	}
	if len(ws.hist.all) != 103 { // init 1 + 空提交 102
		t.Fatalf("allCommits after auto-append = %d, want 103", len(ws.hist.all))
	}
	if !ws.hist.done {
		t.Error("commitsDone should be set when last page is short")
	}
	if len(ws.hist.commits) != 103 {
		t.Fatalf("visible commits = %d, want 103 (append keeps filter view in sync)", len(ws.hist.commits))
	}
}

// 懒加载契约：不在提交历史页时切仓库不加载 git 历史；切到历史页才加载。
func TestHistoryLazyLoad(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.mainTab = 0
	ws.activeRepo = root
	// 工作区页切仓库：不加载历史
	ws.switchActiveRepo(root)
	if ws.hist.all != nil {
		t.Fatal("history loaded while on workspace tab")
	}

	// 切到提交历史页渲染一帧：首页加载（headless 同步）
	ws.mainTab = 1
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if ws.hist.all == nil || len(ws.hist.all) != 1 {
		t.Fatalf("history not loaded on history tab: allCommits=%v", ws.hist.all)
	}
}

// 查找条不被折叠分隔条盖住：diff 顶部有分隔条时，查找条照常可点，点击也不会
// 落到分隔条的展开按钮上（框架把 diff 的条画在分隔条之上；Tester.Click 会
// 拒绝被其他控件遮挡的目标）。
func TestFindBarClearsSeparator(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	// 30 行进基线提交，随后只在末尾追加一行：前 27 行折叠成顶部分隔条
	body := ""
	for i := range 30 {
		body += fmt.Sprintf("line %d\n", i)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(body+"changed line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.mainTab = 1
	ws.hist.sel = 0
	ws.activeRepo = root
	ws.hist.commits = []git.CommitRow{{Hash: "h", ShortHash: "h", Author: "a", Date: "d", Subject: "s"}}
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	// 这里复用提交页装不下 diff：直接走工作区页打开 diff
	ws.mainTab = 0
	ws.hist.all = nil
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	if len(ws.changes) == 0 {
		t.Fatal("no changes found")
	}
	ws.openDiff(ws.changes[0])
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	// 打开内置查找条
	ws.diffv.ed.Find("line")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("unmodified lines") {
		t.Fatal("the diff shows no separator, which the bar could be under")
	}

	// 点中查找条的开关：被分隔条盖住时 Tester.Click 报错，点击落到分隔条上
	// 则会展开折叠行、分隔条的文案消失
	if err := tt.Click("Match case"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("unmodified lines") {
		t.Error("the click on the find bar expanded the separator under it")
	}
}

// 重载保留选中：离开应用再回来（焦点触发的重载）时，选中的提交还在就依旧
// 选中并展示详情；提交没了才回到未选中。
func TestReloadKeepsSelectedCommit(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	for _, msg := range []string{"second", "third"} {
		if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte(msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "commit", "-am", msg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit %s: %v\n%s", msg, err, out)
		}
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.mainTab = 1
	ws.activeRepo = root
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if len(ws.hist.commits) != 3 {
		t.Fatalf("commits = %d, want 3", len(ws.hist.commits))
	}
	// 选中最新提交并展示详情
	head := ws.hist.commits[0].Hash
	ws.hist.sel = 0
	ws.loadCommitDetail(ws.hist.commits[0])
	waitFor(t, func() bool { return !ws.hist.detailLoading && ws.hist.detailText != "" }, 5*time.Second, "detail not loaded")

	// 离开再回来：焦点触发的刷新重载列表
	ws.refresh()
	if ws.hist.sel != 0 || ws.hist.commits[0].Hash != head {
		t.Fatalf("selection after reload = %d, want the commit %s at 0", ws.hist.sel, head)
	}
	waitFor(t, func() bool { return !ws.hist.detailLoading && ws.hist.detailText != "" }, 5*time.Second, "detail not restored after reload")

	// 提交不在了（回退掉最新一条）：选中随之清空
	cmd := exec.Command("git", "reset", "--hard", "HEAD~1")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reset: %v\n%s", err, out)
	}
	ws.refresh()
	if ws.hist.sel != -1 {
		t.Fatalf("selection after the commit went away = %d, want -1", ws.hist.sel)
	}
}

// 焦点刷新的历史页比对：历史没变整体不动（选中/详情/搜索态/分支状态原样，
// 不重读详情），真有新提交才重置落地——实抓：例行重扫走了失效重载，选中
// 与详情闪断、搜索全量态（allTried）被清导致回页重新全量 log。
func TestFocusRefreshKeepsUnchangedHistory(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "commit", "-am", "second")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.mainTab = 1
	ws.activeRepo = root
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if len(ws.hist.commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(ws.hist.commits))
	}
	// 选中一条并展示详情；埋下「历史未变也不得被清」的哨兵
	ws.hist.sel = 0
	ws.loadCommitDetail(ws.hist.commits[0])
	waitFor(t, func() bool { return !ws.hist.detailLoading && ws.hist.detailText != "" }, 5*time.Second, "detail not loaded")
	detail := ws.hist.detailText
	ws.hist.allTried = true
	ws.setBranchStatus(root, git.BranchStatus{Ahead: 3})

	ws.refresh()

	if ws.hist.sel != 0 || ws.hist.detailText != detail || ws.hist.detailLoading {
		t.Fatalf("history state churned on unchanged refresh: sel=%d detail=%q loading=%v",
			ws.hist.sel, ws.hist.detailText, ws.hist.detailLoading)
	}
	if !ws.hist.allTried {
		t.Error("allTried cleared by unchanged refresh")
	}
	if ws.hist.statusByRepo[root].Ahead != 3 {
		t.Errorf("branch status reset by unchanged refresh: %+v", ws.hist.statusByRepo)
	}

	// 真有新提交：重置落地，新提交出现在列表
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "commit", "-am", "third")
	ws.refresh()
	if len(ws.hist.commits) != 3 || !hasSubject(ws.hist.commits, "third") {
		t.Fatalf("commits after real change = %+v, want 3 with third", ws.hist.commits)
	}
}

// 翻页进度保留：已加载的追加页（虚拟列表滚动加载进 all 的行）在历史未变
// 的刷新后不丢——失效重载会把 all 打回首页。
func TestReloadCommitsKeepsLoadedPages(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.mainTab = 1
	ws.activeRepo = root
	ws.loadCommits()
	if len(ws.hist.all) != 1 {
		t.Fatalf("all = %d rows, want 1", len(ws.hist.all))
	}
	// 模拟翻出的追加页（追加不改下标，done 未置）
	ws.hist.all = append(ws.hist.all, git.CommitRow{Hash: "page2"})
	ws.hist.done = false

	ws.refresh()

	if len(ws.hist.all) != 2 || ws.hist.all[1].Hash != "page2" {
		t.Errorf("all = %+v, appended page lost by unchanged refresh", ws.hist.all)
	}
	if ws.hist.done {
		t.Error("done flipped by unchanged refresh")
	}
}
