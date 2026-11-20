package git

import (
	"testing"
)

// 无上游仓库：只返回当前分支，没有 ahead/behind。
func TestReadBranchStatusNoUpstream(t *testing.T) {
	root := filepathToSlash(t.TempDir())
	initRepo(t, root)

	st, err := ReadBranchStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || st.Upstream != "" || st.Ahead != 0 || st.Behind != 0 || st.Gone {
		t.Errorf("status = %+v, want main with no upstream", st)
	}
}

// 上游跟踪：ahead/behind 随本地与远程提交变化，上游删除后标记 Gone。
func TestReadBranchStatusTracking(t *testing.T) {
	origin, work := clonePair(t)

	st, err := ReadBranchStatus(work)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || st.Upstream != "origin/main" || st.Ahead != 0 || st.Behind != 0 {
		t.Fatalf("status = %+v, want main tracking origin/main in sync", st)
	}

	// 本地领先
	commitFile(t, work, "local.txt", "local\n", "local work")
	st, err = ReadBranchStatus(work)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Errorf("status = %+v, want ahead 1 behind 0", st)
	}

	// 远程前进后再 fetch：本地领先且落后
	commitFile(t, origin, "remote.txt", "remote\n", "remote work")
	gitIn(t, origin, "push")
	if err := FetchRepo(work); err != nil {
		t.Fatal(err)
	}
	st, err = ReadBranchStatus(work)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 1 || st.Behind != 1 {
		t.Errorf("status = %+v, want ahead 1 behind 1", st)
	}

	// 上游分支被删除并 prune：Gone 标记优先于 ahead/behind
	gitIn(t, work, "checkout", "-b", "feature")
	gitIn(t, work, "push", "-u", "origin", "feature")
	gitIn(t, work, "push", "origin", "--delete", "feature")
	gitIn(t, work, "fetch", "--prune")
	st, err = ReadBranchStatus(work)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "feature" || !st.Gone {
		t.Errorf("status = %+v, want feature with gone upstream", st)
	}
}

// detached HEAD 没有当前分支。
func TestReadBranchStatusDetached(t *testing.T) {
	_, work := clonePair(t)
	gitIn(t, work, "checkout", "--detach", "HEAD")

	st, err := ReadBranchStatus(work)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "" || st.Upstream != "" || st.Ahead != 0 || st.Behind != 0 {
		t.Errorf("status = %+v, want zero value for detached HEAD", st)
	}
}

// 批量读取：每个仓库一条记录；失败仓库为零值，不中断其它仓库。
func TestReadBranchStatuses(t *testing.T) {
	_, work := clonePair(t)
	plain := filepathToSlash(t.TempDir()) + "/plain"
	initRepo(t, plain)

	repos := []Repo{
		{Path: work, Name: "work", Rel: "work"},
		{Path: plain, Name: "plain", Rel: "plain"},
		{Path: "/missing/repo", Name: "missing", Rel: "missing"},
	}
	got := ReadBranchStatuses(repos)
	if len(got) != len(repos) {
		t.Fatalf("statuses = %d, want %d", len(got), len(repos))
	}
	if st := got[work]; st.Branch != "main" || st.Upstream != "origin/main" {
		t.Errorf("work status = %+v, want main tracking origin/main", st)
	}
	if st := got[plain]; st.Branch != "main" || st.Upstream != "" {
		t.Errorf("plain status = %+v, want main with no upstream", st)
	}
	if st := got["/missing/repo"]; st.Branch != "" {
		t.Errorf("missing status = %+v, want zero value", st)
	}
}
