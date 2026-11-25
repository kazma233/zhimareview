package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"zhimareview/internal/git"
)

// 侧栏全局搜索的范围跟随侧栏 tab：diff列表 只搜改动文件（名字/目录/内容），
// 目录列表 搜全部文件（含未跟踪）。输入即时搜名字，回车搜内容；结果按
// 目录层级树展示，目录名命中独立成行；点击命中打开；清空恢复原列表。

func searchWorkspace(t *testing.T) (*workspace, *ui.Tester, string) {
	t.Helper()
	root := filepath.ToSlash(t.TempDir())
	run := func(dir string, args ...string) {
		if _, err := git.Run(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	// 仓库 utils：needle_dir 目录与文件内容都含 "needle"
	repo := root + "/utils"
	if err := os.MkdirAll(repo+"/needle_dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo+"/needle_docs", 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, c string) {
		if err := os.WriteFile(repo+"/"+p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("needle_dir/a.txt", "hello needle world\n")
	write("plain.txt", "nothing here\n")
	write("hit.txt", "has needle inside\n") // 已提交且无改动：changes 模式搜不到
	write("needle_docs/keep.txt", "keep\n") // 目录名命中（文件名与内容不含词）
	if err := os.MkdirAll(repo+"/chainX/chainY", 0o755); err != nil {
		t.Fatal(err)
	}
	// 纯过路链：chainX/chainY 段名不含词、无分叉，拍平时合并成一行
	write("chainX/chainY/needledeep.txt", "deep needle\n")
	run(root, "init") // 根目录不是仓库（无 .git）
	run(repo, "init")
	// 仓库级身份：夹具的 commit 不依赖全局 git 配置
	run(repo, "config", "user.name", "t")
	run(repo, "config", "user.email", "t@t")
	run(repo, "add", ".")
	run(repo, "commit", "-m", "init")

	// 两处未暂存改动：changes 模式的搜索范围测试用
	appendLine := func(p, line string) {
		f, err := os.OpenFile(filepath.Join(repo, p), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	appendLine("plain.txt", "needle in plain\n")
	appendLine("needle_dir/a.txt", "more needle\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	return ws, tt, repo
}

// 目录列表模式：搜全部文件（含未跟踪）与目录名；结果按目录层级树展示，
// 目录名命中独立成行（哪怕目录下没有命中文件）。
func TestSearchNamesAndDirs(t *testing.T) {
	ws, tt, repo := searchWorkspace(t)
	ws.sidebarTab = "dir"

	ws.search.query = "needle"
	ws.onSearchQueryChanged()
	tt.Frame()
	tt.Frame()

	// 文件名命中：needle_dir/a.txt；目录名命中：needle_dir、needle_docs
	// 目录列表模式不带 git 状态（哪怕文件恰好有改动）
	foundFile, foundDir, withStatus := false, false, false
	for _, h := range ws.search.hits {
		if h.path == "needle_dir/a.txt" {
			foundFile = true
			if h.code != "" {
				withStatus = true
			}
		}
		if h.dir && h.path == "needle_docs" {
			foundDir = true
		}
	}
	if withStatus {
		t.Fatalf("dir mode hit carries git status, want none")
	}
	if !foundFile {
		t.Fatalf("hits = %+v, want needle_dir/a.txt", ws.search.hits)
	}
	if !foundDir {
		t.Fatalf("hits = %+v, want needle_docs dir-name hit", ws.search.hits)
	}

	// 结果树：needle_docs 是 branch 行（目录名命中独立成行），文件 a.txt
	// 嵌在 needle_dir 目录链下（不是平铺的完整路径行）
	rows := buildSearchRows(ws.search.hits, ws.search.openDirs)
	var needleDocsRow, fileRow, repoRow *sideRow
	for i := range rows {
		if rows[i].branch && rows[i].label == "needle_docs" {
			needleDocsRow = &rows[i]
		}
		// 仓库锚行在最顶层（depth 0）：仓库名是 branch，目录链与文件嵌在它下面
		if rows[i].branch && rows[i].label == "utils" && rows[i].depth == 0 {
			repoRow = &rows[i]
		}
		if !rows[i].branch && rows[i].label == "a.txt" {
			fileRow = &rows[i]
		}
	}
	if repoRow == nil {
		t.Fatalf("rows = %+v, want repo anchor row at top", rows)
	}
	// 纯过路链压缩：chainX/chainY 合并成一行 branch，不出现单独的 chainX 行
	chainRow, chainSeg := (*sideRow)(nil), false
	for i := range rows {
		if rows[i].branch && rows[i].label == "chainX/chainY" {
			chainRow = &rows[i]
		}
		if rows[i].branch && rows[i].label == "chainX" {
			chainSeg = true
		}
	}
	if chainRow == nil || chainSeg {
		t.Fatalf("rows = %+v, want compacted chainX/chainY branch without lone chainX", rows)
	}
	// 压缩链的文件叶子嵌在其下
	leafUnder := false
	for i := range rows {
		if !rows[i].branch && rows[i].label == "needledeep.txt" && rows[i].depth == chainRow.depth+1 {
			leafUnder = true
		}
	}
	if !leafUnder {
		t.Fatalf("needledeep.txt not under compacted chain row")
	}
	if needleDocsRow == nil {
		t.Fatalf("rows = %+v, want needle_docs branch row", rows)
	}
	if !needleDocsRow.nameHit {
		t.Fatalf("needle_docs row not marked as name hit")
	}
	if fileRow == nil {
		t.Fatalf("rows = %+v, want a.txt leaf row", rows)
	}
	// a.txt 的深度必须大于 needle_dir 目录行的深度（嵌在目录链下）
	dirDepth := -1
	for i := range rows {
		if rows[i].branch && rows[i].label == "needle_dir" {
			dirDepth = rows[i].depth
		}
	}
	if dirDepth < 0 || fileRow.depth != dirDepth+1 {
		t.Fatalf("a.txt depth %v, want needle_dir depth %v + 1", fileRow.depth, dirDepth)
	}

	// branch 行点击折叠：折叠后子行从拍平结果里消失
	if ws.search.openDirs == nil {
		ws.search.openDirs = map[string]bool{}
	}
	ws.search.openDirs[repo+"/needle_dir"] = false
	rows = buildSearchRows(ws.search.hits, ws.search.openDirs)
	for i := range rows {
		if rows[i].label == "a.txt" {
			t.Fatalf("file row still present after collapsing the dir")
		}
	}
	_ = repo
}

// diff列表模式：名字与内容都限定在改动文件里（外部仓库其余文件不参与）。
func TestSearchDiffScope(t *testing.T) {
	ws, tt, _ := searchWorkspace(t)
	// sidebarTab 默认 "changes"

	ws.search.query = "needle"
	ws.onSearchQueryChanged()
	tt.Frame()
	tt.Frame()

	// 名字：改动文件 needle_dir/a.txt 命中；目录段 needle_dir 命中；
	// needle_docs（目录名含词但无改动文件）不命中
	hasDirHit := false
	for _, h := range ws.search.hits {
		if h.dir && h.path == "needle_docs" {
			hasDirHit = true
		}
	}
	if hasDirHit {
		t.Fatalf("needle_docs dir hit in changes scope, want changes-only")
	}
	foundFile, withStatus := false, false
	for _, h := range ws.search.hits {
		if h.path == "needle_dir/a.txt" {
			foundFile = true
			// 改动文件命中带 git 状态（渲染成状态字母与状态色）
			if h.code == "M" && h.section == "unstaged" {
				withStatus = true
			}
		}
	}
	if !foundFile || !withStatus {
		t.Fatalf("hits = %+v, want changed file hit with M/unstaged status", ws.search.hits)
	}
	// 拍平行带状态字母与等级：M → rankMod
	rows := buildSearchRows(ws.search.hits, ws.search.openDirs)
	statusRow := false
	for i := range rows {
		if rows[i].label == "a.txt" && rows[i].letter == "M" && rows[i].rank == rankMod {
			statusRow = true
		}
	}
	if !statusRow {
		t.Fatalf("rows = %+v, want a.txt row with M letter and rankMod", rows)
	}

	// 内容（回车）：改动文件命中，无改动的 hit.txt 不出现
	ws.runContentSearch()
	tt.Frame()
	tt.Frame()
	for _, h := range ws.search.contentHits {
		if h.path == "hit.txt" {
			t.Fatalf("hit.txt content hit in changes scope, want changed files only")
		}
	}
	foundPlain := false
	for _, h := range ws.search.contentHits {
		if h.path == "plain.txt" && h.line == 2 {
			foundPlain = true
		}
	}
	if !foundPlain {
		t.Fatalf("content hits = %+v, want plain.txt:2", ws.search.contentHits)
	}

	// 清空恢复：侧栏回到原列表
	ws.search.query = ""
	ws.onSearchQueryChanged()
	tt.Frame()
	if ws.searching() {
		t.Fatalf("searching() = true after clearing query")
	}
}

// 目录列表模式的内容搜索（回车）：全仓库命中，含未跟踪文件。
func TestSearchContentOnSubmit(t *testing.T) {
	ws, tt, _ := searchWorkspace(t)
	ws.sidebarTab = "dir"

	ws.search.query = "needle"
	ws.onSearchQueryChanged()
	tt.Frame()
	// 回车前没有内容命中
	if len(ws.search.contentHits) != 0 || ws.search.contentDone {
		t.Fatalf("content hits before submit: %+v", ws.search.contentHits)
	}

	ws.runContentSearch()
	tt.Frame()
	tt.Frame()

	if !ws.search.contentDone {
		t.Fatalf("contentDone = false after submit")
	}
	paths := map[string]bool{}
	for _, h := range ws.search.contentHits {
		paths[h.path] = true
	}
	// keep.txt 内容不含词：不命中（needle_docs 只在名字/目录命中里出现）
	for _, want := range []string{"needle_dir/a.txt", "hit.txt", "plain.txt"} {
		if !paths[want] {
			t.Fatalf("content hits lack %s: %v", want, paths)
		}
	}
	if paths["needle_docs/keep.txt"] {
		t.Fatalf("keep.txt should not match, hits = %v", paths)
	}
}

// 结果点击文件叶子打开文件（复用 openFile 管线，加载完成后生效）。
func TestSearchOpenFileHit(t *testing.T) {
	ws, tt, _ := searchWorkspace(t)
	ws.sidebarTab = "dir"
	ws.search.query = "needle"
	ws.onSearchQueryChanged()
	tt.Frame()
	tt.Frame()

	ws.openFile(ws.root + "/utils/needle_dir/a.txt")
	tt.Frame()
	tt.Frame()
	if ws.dirs.file == nil {
		t.Fatalf("file view did not open after clicking a file hit")
	}
}

// 搜索入口交互：常态渲染 Segmented + 放大镜；点放大镜进入搜索态（输入框
// 占满整行、Segmented 让位、范围锁定）；点 × 清空全部搜索状态并恢复。
func TestSearchOpenClose(t *testing.T) {
	ws, tt, _ := searchWorkspace(t)

	// 常态：有 Segmented，没有搜索框
	if tt.HasText("搜索文件 / 目录，回车搜内容") {
		t.Fatalf("search field visible in normal state")
	}

	// 点放大镜 → 搜索态
	if err := tt.Click("搜索文件与内容"); err != nil {
		t.Fatalf("click search icon: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !ws.search.open {
		t.Fatalf("open = false after clicking search icon")
	}
	if tt.HasText("目录列表") {
		t.Fatalf("segmented still visible in search state; want replaced by search field")
	}

	// 搜索态下输入并搜出结果
	ws.search.query = "needle"
	ws.onSearchQueryChanged()
	ws.runContentSearch()
	tt.Frame()
	tt.Frame()
	if !ws.searching() {
		t.Fatalf("results not shown in search state")
	}

	// Esc 退出搜索态：清空全部状态并恢复 Segmented
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if ws.search.open || ws.search.active != "" || ws.search.query != "" ||
		len(ws.search.hits) != 0 || len(ws.search.contentHits) != 0 || ws.search.contentDone {
		t.Fatalf("search state not cleared: %+v", ws.search)
	}
	if !tt.HasText("目录列表") {
		t.Fatalf("segmented not restored after close")
	}
}

// 内容命中点击后跳到命中的行：行号不只在结果里展示，打开文件要定位到它。
func TestSearchContentHitJumpsToLine(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	repo := root + "/repo"
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for i := 1; i <= 300; i++ {
		if i == 250 {
			body.WriteString("needle here\n")
			continue
		}
		fmt.Fprintf(&body, "line %d\n", i)
	}
	if err := os.WriteFile(repo+"/long.txt", []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "init"}} {
		if _, err := git.Run(repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.applyScanState(root, git.FindRepos(root), git.CollectChanges(git.FindRepos(root)))
	ws.sidebarTab = "dir"
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	ws.search.query = "needle"
	ws.onSearchQueryChanged()
	ws.runContentSearch()
	tt.Frame()
	tt.Frame()
	if len(ws.search.contentHits) != 1 || ws.search.contentHits[0].line != 250 {
		t.Fatalf("content hits = %+v, want the one at line 250", ws.search.contentHits)
	}

	// 点击内容命中行：打开文件并跳到该行
	if err := tt.Click("250 needle here"); err != nil {
		t.Fatalf("click content hit: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if ws.dirs.file == nil || ws.dirs.file.ed == nil {
		t.Fatal("the file did not open")
	}
	ed := ws.dirs.file.ed
	top := float32(ed.LineTop(249))
	bottom := top + float32(ed.LineTop(250)-float64(top))
	if float32(ed.ScrollY()) > top || float32(ed.ScrollY())+float32(ed.ViewportHeight()) < bottom {
		t.Fatalf("the view scrolled to %v, which doesn't show line 250 at %v", ed.ScrollY(), top)
	}
}

// 「查看 diff」下点击内容命中：打开 diff 并定位到新侧命中的行。
func TestSearchContentHitJumpsInDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	repo := root + "/repo"
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&body, "line %d\n", i)
	}
	if err := os.WriteFile(repo+"/long.txt", []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "init"}} {
		if _, err := git.Run(repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	// 工作区把第 250 行改成需要的内容：内容命中在该行，diff 的新侧同样在
	changed := strings.Replace(body.String(), "line 250\n", "needle here\n", 1)
	if err := os.WriteFile(repo+"/long.txt", []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.applyScanState(root, git.FindRepos(root), git.CollectChanges(git.FindRepos(root)))
	ws.sidebarTab = "dir"
	ws.diffPrefs().ShowDiff = true
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	ws.search.query = "needle"
	ws.onSearchQueryChanged()
	ws.runContentSearch()
	tt.Frame()
	tt.Frame()
	if len(ws.search.contentHits) != 1 || ws.search.contentHits[0].line != 250 {
		t.Fatalf("content hits = %+v, want the one at line 250", ws.search.contentHits)
	}

	for _, seg := range []int{0, 1} {
		// 统一/分栏都定位到新侧的行：布局是全局偏好，打开即按它渲染
		ws.diffPrefs().Layout = seg
		// 行选区归位：叶子行保留选中，同一行再点不触发点击（treeList 契约）
		ws.search.selList.sel = -1
		if err := tt.Click("250 needle here"); err != nil {
			t.Fatalf("layout %d: click content hit: %v", seg, err)
		}
		// 打开 diff（帧内启动加载）+ 量好行高的一帧 + 定位生效
		for range 4 {
			tt.Frame()
		}
		if ws.diffv.open == nil || ws.diffv.sides == nil {
			t.Fatal("the diff did not open")
		}
		d := &ws.diffv.ed
		top := float32(d.LineTop(editor.DiffNew, 249))
		if float32(d.ScrollY()) > top || float32(d.ScrollY())+float32(d.ViewportHeight()) < top {
			t.Fatalf("layout %d: the diff scrolled to %v, which doesn't show the hit's line at %v", seg, d.ScrollY(), top)
		}
	}
}

// 空态文案跟状态机走：名字搜索完成零命中显示「没有文件名命中」（不再卡
// 「搜索中…」）；回车内容搜索完成后（headless 同步管线直达终态）零命中
// 是「没有匹配」。
func TestSearchEmptyStateProgression(t *testing.T) {
	ws, tt, _ := searchWorkspace(t)
	ws.sidebarTab = "changes"

	// 输入无命中的词：名字搜索（changes 同步过滤）立即完成
	ws.search.query = "zzz-none"
	ws.onSearchQueryChanged()
	tt.Frame()
	tt.Frame()
	if !ws.search.nameDone {
		t.Fatal("name search not done after synchronous filter")
	}
	if !tt.HasText("没有文件名命中") || tt.HasText("搜索中…") {
		t.Fatalf("want 'no name hit' state, texts=%v", tt.Texts())
	}

	// 回车：内容搜索（headless 同步管线）直达终态，零命中是「没有匹配」
	ws.search.active = ws.search.query
	ws.runContentSearch()
	tt.Frame()
	tt.Frame()
	if ws.search.contentRunning {
		t.Fatal("content search still running after sync pipeline")
	}
	if !tt.HasText("没有匹配") || tt.HasText("搜索中…") {
		t.Fatalf("want 'no match' state, texts=%v", tt.Texts())
	}
}
