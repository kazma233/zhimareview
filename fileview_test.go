package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"zhimareview/internal/git"
)

// 复制路径：diff 列表与目录列表的行右键都复制绝对路径；文件查看头部
// 不再放复制按钮。
func TestCopyFilePath(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	change := ws.changes[0]

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	if err := tt.RightClick("base.txt"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制路径"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got, want := tt.Clipboard(), changeAbs(change); got != want {
		t.Errorf("row clipboard = %q, want %q", got, want)
	}

	// 打开文件查看：头部没有复制按钮，复制入口在目录列表行右键
	ws.openFile(root + "/base.txt")
	waitFor(t, func() bool { return ws.dirs.file != nil && !ws.dirs.file.loading && ws.dirs.file.kind == "text" },
		5*time.Second, "file not loaded")
	tt.Frame()
	if tt.HasText("复制路径") {
		t.Error("file header should not show a copy-path button")
	}

	ws.sidebarTab = "dir"
	ws.dirs.cache = map[string][]fsEntry{root: readDirEntries(root)}
	ws.dirs.rebuildRows(root, ws.changes)
	tt.Frame()
	if err := tt.RightClick("base.txt"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制路径"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got, want := tt.Clipboard(), root+"/base.txt"; got != want {
		t.Errorf("dir row clipboard = %q, want %q", got, want)
	}
}

// 目录列表打开文件的分流契约：「查看 diff」勾选且带改动的文件直接进 diff
// （展开态取全局偏好，ws.file 让位）；不勾选（默认）时带改动的文件也只读
// 查看原内容；无改动的文件任何状态下都走只读查看（editor 状态加载完成）。
func TestOpenFileRoutesToDiff(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	// clean.txt 提交后保持无改动；changed.txt 提交后再改出未暂存改动
	if err := os.WriteFile(filepath.Join(root, "clean.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\nv3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))

	// 默认不勾选「查看 diff」：带改动的文件也只读查看原内容
	if ws.diffPrefs().ShowDiff {
		t.Fatal("showDiff should default to false")
	}
	ws.openFile(root + "/changed.txt")
	if ws.diffv.open != nil || ws.dirs.file == nil {
		t.Errorf("changed file should open read-only when 查看diff is off: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")

	// 无改动 → 只读查看（勾选与否都一样）
	ws.openFile(root + "/clean.txt")
	if ws.diffv.open != nil || ws.dirs.file == nil {
		t.Errorf("clean file should open as read-only view: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")

	// 勾选「查看 diff」（全局偏好）：带改动 → diff，文件查看让位；
	// 展开态同样按全局偏好（这里设为展开）
	ws.diffPrefs().ShowDiff = true
	ws.diffPrefs().Expand = true
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.open == nil || ws.dirs.file != nil {
		t.Errorf("changed file should open as diff: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
	if !ws.diffv.ed.ExpandUnchanged {
		t.Error("dir-opened diff should follow the global expand pref")
	}
}

// 「查看 diff」checkbox 跟随主区内容出现：带改动的文件（只读查看）与目录
// 入口的 diff 头部有，无改动文件与 diff 列表入口的 diff 没有。
func TestDirShowDiffCheckbox(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "clean.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\nv3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	tt := ui.NewTester(ws.view, 1180, 800)

	// 带改动文件只读查看：头部出现 checkbox
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")
	tt.Frame()
	if !tt.HasText("查看 diff") {
		t.Fatal("changed file view should show 查看diff checkbox")
	}

	// 无改动文件：没有 checkbox
	ws.openFile(root + "/clean.txt")
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")
	tt.Frame()
	if tt.HasText("查看 diff") {
		t.Error("clean file view should not show 查看diff checkbox")
	}

	// 目录入口的 diff：头部出现 checkbox（全局「查看 diff」勾选）
	ws.diffPrefs().ShowDiff = true
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	tt.Frame()
	if !tt.HasText("查看 diff") {
		t.Error("dir-opened diff should show 查看diff checkbox")
	}

	// diff 列表入口的 diff：没有 checkbox（开关不管辖）
	ws.openDiff(ws.changes[0])
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	tt.Frame()
	if tt.HasText("查看 diff") {
		t.Error("changes-tab diff should not show 查看diff checkbox")
	}
}

// 翻转「查看 diff」时已打开的主区内容跟着切换：勾选把目录列表打开的只读
// 文件切到 diff；取消把目录入口打开的 diff 切回只读查看；diff 列表入口的
// diff 不受开关管辖，保持不动。
func TestDirShowDiffRetarget(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("v2\nv3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()

	// 默认只读打开带改动文件，勾选后主区切到 diff（展开态取全局偏好）
	ws.openFile(root + "/changed.txt")
	waitFor(t, func() bool { return !ws.dirs.file.loading && ws.dirs.file.kind == "text" }, 5*time.Second, "file not loaded")
	tt.Frame() // checkbox 在内容头部：主区打开后重建帧才能点击
	if err := tt.Click("查看 diff"); err != nil {
		t.Fatalf("click 查看diff: %v", err)
	}
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.open == nil || ws.dirs.file != nil || !ws.diffv.fromDir {
		t.Errorf("checking 查看diff should retarget file to diff: diff=%v file=%v fromDir=%v",
			ws.diffv.open != nil, ws.dirs.file != nil, ws.diffv.fromDir)
	}

	// 取消勾选后目录入口的 diff 切回只读查看
	if err := tt.Click("查看 diff"); err != nil {
		t.Fatalf("click 查看diff again: %v", err)
	}
	if ws.diffv.open != nil || ws.dirs.file == nil {
		t.Errorf("unchecking 查看diff should retarget diff to file: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}

	// diff 列表入口打开的 diff 不受开关管辖：头部没有 checkbox，无从翻转
	ws.diffPrefs().ShowDiff = true
	ws.openDiff(ws.changes[0])
	waitFor(t, func() bool { return !ws.diffv.loading && ws.diffv.sides != nil }, 5*time.Second, "diff not loaded")
	if ws.diffv.fromDir {
		t.Fatal("changes-tab diff should not be marked dir-opened")
	}
	tt.Frame()
	if tt.HasText("查看 diff") {
		t.Error("changes-tab diff should not show 查看diff checkbox")
	}
	if ws.diffv.open == nil || ws.dirs.file != nil {
		t.Errorf("changes-tab diff should stay open: diff=%v file=%v", ws.diffv.open != nil, ws.dirs.file != nil)
	}
}

// 只读图片预览的缩放与聚焦：打开请求把键盘焦点送进图片（无需先点一下），
// 方向键直接平移；滚轮放大；同内容 watch 重读保留缩放。
func TestFileImageZoom(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	writePNG(t, root, "pic.png", pngBytes(t, 0, 128, 255))

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.openFile(root + "/pic.png")
	waitFor(t, func() bool {
		return ws.dirs.file != nil && !ws.dirs.file.loading && ws.dirs.file.kind == "image"
	}, 5*time.Second, "image not loaded")

	// 焦点随绑定在同一帧落地；视口尺寸由绘制写入，之后交互即可用
	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	if !tt.Focused("图片预览") {
		t.Fatal("opening an image must focus it without an extra click")
	}
	// 先放大再平移：适配态（scale=1）平移被钳回居中
	tt.Wheel(600, 400, 0, -40)
	if ws.dirs.file.imgZoom.scale <= 1 {
		t.Fatalf("wheel should zoom the image, scale = %v", ws.dirs.file.imgZoom.scale)
	}
	tt.Key(0, ui.KeyDown) // 焦点已在图片：方向键直接平移
	if v := ws.dirs.file.imgZoom.y; v >= 0 {
		t.Fatalf("arrow key should pan the focused image, y = %v", v)
	}

	// 同内容重读（watch 命中）：缩放保留，不闪回适配态
	ws.reloadOpenFile()
	waitFor(t, func() bool { return ws.dirs.file != nil && !ws.dirs.file.loading }, 5*time.Second, "reload not done")
	if ws.dirs.file.imgZoom.scale <= 1 {
		t.Fatal("same-content reload must keep the zoom")
	}

	// 焦点被移走后，同内容重读不得抢回（Tab 自证焦点确实离开）
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if tt.Focused("图片预览") {
		t.Skip("Tab did not move focus away in this view; reload-steal not observable")
	}
	ws.reloadOpenFile()
	waitFor(t, func() bool { return ws.dirs.file != nil && !ws.dirs.file.loading }, 5*time.Second, "reload not done")
	tt.Frame()
	tt.Frame()
	if tt.Focused("图片预览") {
		t.Fatal("same-content reload stole the focus back to the image")
	}
}

// 只读文本查看：打开请求同样把焦点送进内容区（edHandle 绑官方编辑器的
// 可聚焦元素），方向键直接移动光标，无需先点一下。
func TestFileTextFocusOnOpen(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	write(t, root+"/note.txt", "one\ntwo\nthree\n")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.openFile(root + "/note.txt")
	waitFileLoaded(t, ws)

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	if !ws.dirs.file.ed.Focused() {
		t.Fatal("opening a text file must focus the editor without an extra click")
	}
	before := ws.dirs.file.ed.Cursor()
	tt.Key(0, ui.KeyDown)
	if ws.dirs.file.ed.Cursor() == before {
		t.Fatal("arrow key must move the cursor of the focused editor")
	}

	// 焦点被移走后，同内容重读不得抢回光标（Tab 自证焦点确实离开）
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	cursor := ws.dirs.file.ed.Cursor()
	tt.Key(0, ui.KeyDown)
	if ws.dirs.file.ed.Cursor() != cursor {
		t.Skip("Tab did not move focus away in this view; reload-steal not observable")
	}
	ws.reloadOpenFile()
	waitFor(t, func() bool { return ws.dirs.file != nil && !ws.dirs.file.loading }, 5*time.Second, "reload not done")
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	if ws.dirs.file.ed.Cursor() != cursor {
		t.Fatal("same-content reload stole the focus back to the editor")
	}
}

// 打开图片后的第一次交互必须生效，且连续打开第二张同样生效：图片出现那一帧
// 的 build 期布局还读不到尺寸，事件期必须用绘制后写入的视口（回归：曾用
// build 期 Bounds 缓存，首次滚轮/按键被静默吞掉——实抓于真实时序）。
func TestFileImageFirstEventAfterOpen(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	gitInit(t, root)
	writePNG(t, root, "a.png", pngBytes(t, 255, 0, 0))
	writePNG(t, root, "b.png", pngBytes(t, 0, 255, 0))

	// 真实窗口时序：load 在后台、apply 经主线程队列落地
	q := &testQueue{}
	ws := newWorkspace(nil)
	ws.post = q.post
	ws.mode = "work"
	ws.root = root
	repos := git.FindRepos(root)
	ws.applyScanState(root, repos, git.CollectChanges(repos))

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("目录列表"); err != nil {
		t.Fatalf("click dir tab: %v", err)
	}
	tt.Frame()
	for i := 0; i < 5; i++ { // 目录树懒加载
		q.drain()
		tt.Frame()
	}

	open := func(name string) {
		t.Helper()
		if err := tt.Click(name); err != nil {
			t.Fatalf("click %s: %v", name, err)
		}
		tt.Frame() // 点击落地：openFileAt 在本次 build 触发
		waitFor(t, func() bool { return q.pending() > 0 }, 5*time.Second, name+" apply not queued")
		q.drain() // applyFile（对应真实窗口的 win.Update）
		tt.Frame() // 图片出现的那一帧，之后立刻交互
		waitFor(t, func() bool {
			return ws.dirs.file != nil && !ws.dirs.file.loading && ws.dirs.file.kind == "image"
		}, 5*time.Second, name+" not loaded")
		if !tt.Focused("图片预览") {
			t.Fatalf("%s: image not focused on open", name)
		}
	}

	open("a.png")
	tt.Wheel(600, 300, 0, -40) // 第一张的第一次滚轮
	if ws.dirs.file.imgZoom.scale <= 1 {
		t.Fatalf("first gesture after opening a.png was swallowed: scale=%v", ws.dirs.file.imgZoom.scale)
	}

	open("b.png")
	tt.Wheel(600, 300, 0, -40) // 第二张的第一次滚轮
	if ws.dirs.file.imgZoom.scale <= 1 {
		t.Fatalf("first gesture after opening b.png was swallowed: scale=%v", ws.dirs.file.imgZoom.scale)
	}
	y := ws.dirs.file.imgZoom.y
	tt.Key(0, ui.KeyDown)
	if ws.dirs.file.imgZoom.y >= y {
		t.Fatalf("arrow key after opening b.png did not pan: y %v -> %v", y, ws.dirs.file.imgZoom.y)
	}
}

// 打开不支持的文件时，聚焦请求就地作废：之后 watch 重读恰好解码成功，
// 不得把焦点突然抢进内容区（久远的打开请求不属于当前会话）。
func TestFileUnsupportedDoesNotStealFocusLater(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	// 扩展名是图片、内容解不出来：先落到 unsupported，聚焦请求随之作废
	write(t, root+"/pic.png", "v1\x00not-a-png")

	ws := newWorkspace(nil)
	ws.mode = "work"
	ws.root = root
	ws.openFile(root + "/pic.png")
	waitFor(t, func() bool {
		return ws.dirs.file != nil && !ws.dirs.file.loading && ws.dirs.file.kind == "unsupported"
	}, 5*time.Second, "unsupported file not loaded")

	tt := ui.NewTester(ws.view, 1180, 800)
	tt.Frame()
	tt.Frame()

	// 磁盘内容改成合法 PNG：重读解码成功，但不该抢焦点
	writePNG(t, root, "pic.png", pngBytes(t, 0, 200, 0))
	ws.reloadOpenFile()
	waitFor(t, func() bool {
		return ws.dirs.file != nil && !ws.dirs.file.loading && ws.dirs.file.kind == "image"
	}, 5*time.Second, "file did not turn into an image")
	tt.Frame()
	tt.Frame()
	if tt.Focused("图片预览") {
		t.Fatal("a long-consumed open request must not steal focus on a later reload")
	}
}
