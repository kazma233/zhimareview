package git

import (
	"strings"
	"testing"
)

func TestLogCommitsAndRefs(t *testing.T) {
	root := filepathToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/f.txt", "v2\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "second commit")
	// 分支与标签成为引用
	gitIn(t, root, "branch", "feature")
	gitIn(t, root, "tag", "v1", "HEAD")

	commits, err := LogCommits(root, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	if commits[0].Subject != "second commit" {
		t.Errorf("subject = %q, want second commit", commits[0].Subject)
	}
	if !strings.Contains(commits[0].Refs, "feature") || !strings.Contains(commits[0].Refs, "v1") {
		t.Errorf("refs = %q, want feature and v1", commits[0].Refs)
	}
	if len(commits[0].ShortHash) == 0 || len(commits[0].Hash) < len(commits[0].ShortHash) {
		t.Errorf("hashes = %q / %q", commits[0].Hash, commits[0].ShortHash)
	}
}

func TestCommitPatchAndMerge(t *testing.T) {
	root := filepathToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/f.txt", "v2\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "second")

	commits, _ := LogCommits(root, 0, 0)
	patch, err := CommitPatch(root, commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+v2") {
		t.Errorf("patch = %q, want +v2", patch)
	}

	merge, err := IsMergeCommit(root, commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if merge {
		t.Error("normal commit should not be a merge")
	}
}

// LogCommits 的 Merge 标记：分支合并提交为 true，普通提交为 false。
func TestLogCommitsMergeFlag(t *testing.T) {
	root := filepathToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/a.txt", "v1\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "base")
	gitIn(t, root, "checkout", "-b", "feature")
	write(t, root+"/a.txt", "v2\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "feature work")
	gitIn(t, root, "checkout", "main")
	write(t, root+"/b.txt", "x\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "master work")
	gitIn(t, root, "merge", "--no-edit", "feature")

	commits, err := LogCommits(root, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	merges := 0
	for _, r := range commits {
		if strings.Contains(r.Subject, "Merge branch") || strings.Contains(r.Subject, "merge") {
			if !r.Merge {
				t.Errorf("commit %q should be marked as merge", r.Subject)
			}
			merges++
		} else if r.Merge {
			t.Errorf("commit %q marked as merge but is not", r.Subject)
		}
	}
	if merges != 1 {
		t.Errorf("merge commits = %d, want 1", merges)
	}
}

func TestRevertCommit(t *testing.T) {
	root := filepathToSlash(t.TempDir())
	initRepo(t, root)
	write(t, root+"/f.txt", "v2\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-m", "second commit")

	commits, _ := LogCommits(root, 0, 0)
	if err := RevertCommit(root, commits[0].Hash); err != nil {
		t.Fatal(err)
	}
	// revert --no-edit 自动提交：工作区干净、历史出现 Revert 提交
	st, err := ReadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Staged)+len(st.Unstaged)+len(st.Untracked) != 0 {
		t.Errorf("status = %+v, want clean after auto-committed revert", st)
	}
	logs, err := LogCommits(root, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(logs[0].Subject, `Revert "second commit"`) {
		t.Errorf("latest subject = %q, want revert commit", logs[0].Subject)
	}
	// f.txt 是该提交新增的文件：revert 后从 HEAD 消失；base.txt 不受影响
	if _, found, _ := ShowObject(root, "HEAD", "f.txt"); found {
		t.Error("f.txt should be gone after reverting its adding commit")
	}
	content, found, _ := ShowObject(root, "HEAD", "base.txt")
	if !found || strings.TrimSpace(content) != "base" {
		t.Errorf("base.txt content = %q, want base", content)
	}
}

// filepathToSlash 是测试夹具的路径归一化。
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
