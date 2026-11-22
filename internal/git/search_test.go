package git

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 搜索的契约：名字与内容都覆盖未跟踪文件；git grep 的 NUL 分隔保证冒号
// 文件名不被解析错位；无匹配与空仓库都是空结果而非错误。

func searchRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.ToSlash(t.TempDir())
	run := func(args ...string) {
		if _, err := Run(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.MkdirAll(dir+"/utils/deep", 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("utils/deep/a.txt", "hello needle world\n")
	write("b.txt", "no match here\n")
	write("odd:name.txt", "colon:name\nneedle inside\n")
	write("u.txt", "untracked needle here\n")
	run("init")
	// 仓库级身份：夹具的 commit 不依赖全局 git 配置
	run("config", "user.name", "t")
	run("config", "user.email", "t@t")
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func TestSearchNames(t *testing.T) {
	dir := searchRepo(t)

	hits := SearchNames(dir, "needle")
	if len(hits) != 0 {
		t.Fatalf("names = %v, want no path contains 'needle'", hits)
	}
	hits = SearchNames(dir, "deep")
	if len(hits) != 1 || hits[0] != "utils/deep/a.txt" {
		t.Fatalf("names = %v, want utils/deep/a.txt", hits)
	}
	// 不分大小写
	hits = SearchNames(dir, "UTILS")
	if len(hits) != 1 {
		t.Fatalf("names = %v, want case-insensitive match", hits)
	}
	// 未跟踪文件也在结果里
	hits = SearchNames(dir, "u.txt")
	if len(hits) != 1 {
		t.Fatalf("names = %v, want untracked file", hits)
	}
	if hits := SearchNames(dir, "zzz"); len(hits) != 0 {
		t.Fatalf("names = %v, want empty for no match", hits)
	}
}

func TestSearchContents(t *testing.T) {
	dir := searchRepo(t)

	// Windows/NTFS 的文件名冒号是备用数据流分隔符："odd:name.txt" 落盘成
	// 文件 odd + 数据流 name.txt，冒号文件名在该平台无法作为常规文件存在，
	// 冒号契约（NUL 分隔不被 path:line 解析错位）不可测，断言按平台收缩
	colonFile := runtime.GOOS != "windows"
	want := 3
	if !colonFile {
		want = 2
	}
	hits := SearchContents(dir, "needle")
	if len(hits) != want {
		t.Fatalf("hits = %+v, want %d lines (tracked + colon file + untracked)", hits, want)
	}
	byPath := map[string]ContentHit{}
	for _, h := range hits {
		byPath[h.Path] = h
	}
	h := byPath["utils/deep/a.txt"]
	if h.Line != 1 || h.Text != "hello needle world" {
		t.Fatalf("a.txt hit = %+v", h)
	}
	if colonFile {
		// 冒号文件名：NUL 分隔下不会被 path:line 解析错位
		h, ok := byPath["odd:name.txt"]
		if !ok || h.Line != 2 || h.Text != "needle inside" {
			t.Fatalf("colon name hit = %+v, want line 2", h)
		}
	}
	h, ok := byPath["u.txt"]
	if !ok || h.Line != 1 {
		t.Fatalf("untracked hit = %+v", h)
	}
	// 不分大小写
	if hits := SearchContents(dir, "NEEDLE"); len(hits) != want {
		t.Fatalf("NEEDLE hits = %d, want %d (case-insensitive)", len(hits), want)
	}
	if hits := SearchContents(dir, "zzz"); len(hits) != 0 {
		t.Fatalf("zzz hits = %v, want empty", hits)
	}
}

func TestSearchContentsEmptyRepo(t *testing.T) {
	dir := filepath.ToSlash(t.TempDir())
	if _, err := Run(dir, "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 空仓库（无提交）：--untracked 下仍能搜到未跟踪文件（git 的回退行为）
	hits := SearchContents(dir, "needle")
	if len(hits) != 1 || hits[0].Path != "x.txt" {
		t.Fatalf("hits = %+v, want the untracked file in empty repo", hits)
	}
	if hits := SearchContents(dir, "zzz"); len(hits) != 0 {
		t.Fatalf("hits = %+v, want empty for no match", hits)
	}
}

func TestSearchAllConcurrent(t *testing.T) {
	dir := searchRepo(t)
	repos := FindRepos(dir)
	if len(repos) != 1 {
		t.Fatalf("repos = %d, want 1", len(repos))
	}
	names := SearchNamesAll(repos, "deep")
	if len(names[dir]) != 1 {
		t.Fatalf("names = %v", names)
	}
	// Windows/NTFS 上冒号文件名不可作为常规文件存在（见 TestSearchContents）
	want := 3
	if runtime.GOOS == "windows" {
		want = 2
	}
	contents := SearchContentsAll(repos, "needle")
	if len(contents[dir]) != want {
		t.Fatalf("contents = %v", contents)
	}
}
