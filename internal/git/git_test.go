package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn 在测试夹具仓库里执行 git 命令，失败即终止测试。
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// initRepo 在 dir 建一个带基线提交（base.txt）的仓库；目录不存在则创建。
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-b", "main")
	// 仓库级身份：夹具的 commit 不依赖全局 git 配置
	gitIn(t, dir, "config", "user.name", "t")
	gitIn(t, dir, "config", "user.email", "t@t")
	write(t, dir+"/base.txt", "base\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "init")
}

// write 创建带内容的文件（自动建父目录）。
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindRepos(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())

	// root 自身是仓库：rel 为空，且继续向下扫描子仓库
	initRepo(t, root)
	initRepo(t, root+"/repo-a")
	initRepo(t, root+"/group/repo-b")
	write(t, root+"/group/plain/file.txt", "x") // 非仓库目录
	// worktree：.git 是文件
	write(t, root+"/source/placeholder.txt", "x")
	initRepo(t, root+"/source")
	gitIn(t, root+"/source", "worktree", "add", root+"/wtree", "-b", "wt")
	// 依赖目录里的伪仓库必须被跳过
	initRepo(t, root+"/node_modules/fake")

	repos := FindRepos(root)
	var rels []string
	for _, r := range repos {
		rels = append(rels, r.Rel)
	}
	want := []string{"", "group/repo-b", "repo-a", "source", "wtree"}
	if len(rels) != len(want) {
		t.Fatalf("rels = %v, want %v", rels, want)
	}
	for i := range want {
		if rels[i] != want[i] {
			t.Errorf("rels[%d] = %q, want %q (all: %v)", i, rels[i], want[i], rels)
		}
	}
	// 根仓库的 name 取路径末段
	if repos[0].Name == "" || repos[0].Rel != "" {
		t.Errorf("root repo = %+v, want empty rel with a name", repos[0])
	}
	// worktree 仓库也入列
	if repos[len(repos)-1].Rel != "wtree" {
		t.Errorf("last repo = %+v, want wtree worktree", repos[len(repos)-1])
	}
}

// 空/损坏的 .git 不是仓库：git 会忽略它并解析到上层仓库，侧栏若按名字登记，
// 会把上层仓库的同一批改动按子仓库再报一遍。无效条目不阻断下探。
func TestFindReposIgnoresInvalidGitEntry(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/stale/file.txt", "x")
	if err := os.MkdirAll(root+"/stale/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root+"/junk/file.txt", "x")
	write(t, root+"/junk/.git", "not a gitdir pointer\n")

	repos := FindRepos(root)
	if len(repos) != 1 || repos[0].Rel != "" {
		t.Fatalf("repos = %+v, want only root repo", repos)
	}

	// 无效 .git 目录按普通目录下探：里面真正的子仓库仍然被发现
	initRepo(t, root+"/stale/inner")
	repos = FindRepos(root)
	rels := make([]string, len(repos))
	for i, r := range repos {
		rels[i] = r.Rel
	}
	if len(rels) != 2 || rels[0] != "" || rels[1] != "stale/inner" {
		t.Errorf("rels = %v, want [\"\" \"stale/inner\"]", rels)
	}
}

func TestFindReposRootNotRepo(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root+"/only")
	repos := FindRepos(root)
	if len(repos) != 1 || repos[0].Rel != "only" || repos[0].Name != "only" {
		t.Errorf("repos = %+v, want only repo with name only", repos)
	}
}

// 隐藏目录（点开头）不下探：里面的伪仓库不入列，可见仓库照常发现。
func TestFindReposSkipsHiddenDirs(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root+"/real")
	initRepo(t, root+"/.idea/fake")
	initRepo(t, root+"/.svn/repos/whatever")
	initRepo(t, root+"/.gradle/cache/repo")

	repos := FindRepos(root)
	if len(repos) != 1 || repos[0].Rel != "real" {
		t.Errorf("repos = %+v, want only visible repo %q", repos, "real")
	}
}

func TestReadStatus(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root)

	// 基线干净
	st, err := ReadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || len(st.Staged)+len(st.Unstaged)+len(st.Untracked) != 0 {
		t.Errorf("clean status = %+v, want main and empty", st)
	}

	// 修改（unstaged）+ 暂存（staged）+ 未跟踪
	write(t, root+"/base.txt", "changed\n")
	write(t, root+"/new.txt", "untracked\n")
	st, err = ReadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Unstaged) != 1 || st.Unstaged[0].Path != "base.txt" || st.Unstaged[0].Y != 'M' {
		t.Errorf("unstaged = %+v, want base.txt M", st.Unstaged)
	}
	if len(st.Untracked) != 1 || st.Untracked[0].Path != "new.txt" {
		t.Errorf("untracked = %+v, want new.txt", st.Untracked)
	}

	gitIn(t, root, "add", ".")
	st, err = ReadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Staged) != 2 {
		t.Errorf("staged = %+v, want base.txt and new.txt", st.Staged)
	}
	if len(st.Unstaged) != 0 || len(st.Untracked) != 0 {
		t.Errorf("after add: unstaged = %+v untracked = %+v, want empty", st.Unstaged, st.Untracked)
	}
}

// 改名条目（git mv）：porcelain v1 一行两个路径（"old -> new"），解析后
// Path 取新路径（文件现所在），旧路径携带在 OrigPath——实抓：不拆分时
// Path 是含箭头的原始串，侧栏按旧目录分组、按新路径打开全部失败。
func TestReadStatusRename(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/aidocs/note.md", "moved\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "base")
	if err := os.MkdirAll(root+"/docs", 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "mv", "aidocs/note.md", "docs/note.md")

	st, err := ReadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Staged) != 1 {
		t.Fatalf("staged = %+v, want 1 rename entry", st.Staged)
	}
	f := st.Staged[0]
	if f.X != 'R' || f.Path != "docs/note.md" || f.OrigPath != "aidocs/note.md" {
		t.Errorf("rename entry = %+v, want R docs/note.md (from aidocs/note.md)", f)
	}

	changes := CollectChanges(FindRepos(root))
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want 1", changes)
	}
	c := changes[0]
	if c.Section != "staged" || c.Code != "R" || c.Path != "docs/note.md" || c.OrigPath != "aidocs/note.md" {
		t.Errorf("change = %+v, want staged R docs/note.md from aidocs/note.md", c)
	}
}

func TestCollectChanges(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root+"/alpha")
	initRepo(t, root+"/beta/deep")
	// 修改已跟踪文件 → M unstaged；新增文件 → untracked
	write(t, root+"/alpha/base.txt", "mod\n")
	write(t, root+"/beta/deep/b.txt", "untracked\n")

	repos := FindRepos(root)
	changes := CollectChanges(repos)
	// 顺序：unstaged → staged → untracked
	want := []Change{
		{root + "/alpha", "alpha", "base.txt", "", "unstaged", "M"},
		{root + "/beta/deep", "beta/deep", "b.txt", "", "untracked", "?"},
	}
	if len(changes) != len(want) {
		t.Fatalf("changes = %+v, want %d entries", changes, len(want))
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Errorf("changes[%d] = %+v, want %+v", i, changes[i], want[i])
		}
	}
}

// 上层仓库跟踪的文件 + 子目录里的空 .git（残留目录）：git 在子目录里解析到
// 上层仓库，改动只能上报一次；修复前会多出一份 RepoRel="sub" 的副本。
func TestCollectChangesIgnoresInvalidGitEntry(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/sub/file.txt", "base\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "sub")
	if err := os.MkdirAll(root+"/sub/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root+"/sub/file.txt", "mod\n")

	changes := CollectChanges(FindRepos(root))
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want 1 duplicate-free entry", changes)
	}
	if c := changes[0]; c.RepoPath != root || c.RepoRel != "" || c.Path != "sub/file.txt" {
		t.Errorf("change = %+v, want root repo sub/file.txt", c)
	}
}

func TestStageUnstageCommit(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/f.txt", "v1\n")

	if err := StageFiles(root, []string{"f.txt"}); err != nil {
		t.Fatal(err)
	}
	st, err := ReadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Staged) != 1 || st.Staged[0].Path != "f.txt" {
		t.Errorf("staged = %+v, want f.txt", st.Staged)
	}

	// 取消暂存：新文件回到未跟踪（从未提交过，不可能回到 unstaged）
	if err := UnstageFiles(root, []string{"f.txt"}); err != nil {
		t.Fatal(err)
	}
	st, _ = ReadStatus(root)
	if len(st.Staged) != 0 || len(st.Untracked) != 1 || st.Untracked[0].Path != "f.txt" {
		t.Errorf("after unstage: staged = %+v untracked = %+v, want f.txt untracked", st.Staged, st.Untracked)
	}

	// 暂存后提交，三区清空
	if err := StageFiles(root, []string{"f.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := CommitRepo(root, "test commit"); err != nil {
		t.Fatal(err)
	}
	st, _ = ReadStatus(root)
	if len(st.Staged)+len(st.Unstaged)+len(st.Untracked) != 0 {
		t.Errorf("after commit status = %+v, want clean", st)
	}
}

func TestShowObject(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	initRepo(t, root)

	content, found, err := ShowObject(root, "HEAD", "base.txt")
	if err != nil || !found {
		t.Fatalf("ShowObject: found=%v err=%v", found, err)
	}
	if strings.TrimSpace(content) != "base" {
		t.Errorf("content = %q, want base", content)
	}

	_, found, err = ShowObject(root, "HEAD", "missing.txt")
	if err != nil || found {
		t.Errorf("missing object: found=%v err=%v, want false nil", found, err)
	}
}
