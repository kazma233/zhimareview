package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"zhimareview/internal/git"
)

// waitFor 轮询等待后台 goroutine 的效果落地（写盘、状态刷新）。
func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// gitInit 在 dir 建带基线提交（base.txt）的仓库（main 包测试的本地夹具）。
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@t"},
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// gitRun 在测试目录执行 git（夹具搭建失败即终止测试）。
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// cloneWithRemote 建本地 bare 远程与两个 clone：origin 用于推送新提交，
// work 是被测仓库（已跟踪 origin/main）。
func cloneWithRemote(t *testing.T) (origin, work string) {
	t.Helper()
	base := filepath.ToSlash(t.TempDir())
	remote := base + "/remote.git"
	seed := base + "/seed"
	gitRun(t, base, "init", "--bare", "-b", "main", remote)
	gitInit(t, seed)
	gitRun(t, seed, "remote", "add", "origin", remote)
	gitRun(t, seed, "push", "-u", "origin", "main")
	origin = base + "/origin"
	work = base + "/work"
	// clone 时单次 -c 关 autocrlf：clone 自带的 checkout 也走 LF，不依赖
	// 机器的系统级配置（事后设 config 救不回 clone 已经写出的 CRLF 工作区）
	gitRun(t, base, "-c", "core.autocrlf=false", "clone", remote, origin)
	gitRun(t, base, "-c", "core.autocrlf=false", "clone", remote, work)
	for _, dir := range []string{origin, work} {
		gitRun(t, dir, "config", "user.name", "t")
		gitRun(t, dir, "config", "user.email", "t@t")
		// autocrlf 关掉：clone 的 checkout 不依赖机器的系统级配置换行行为
		gitRun(t, dir, "config", "core.autocrlf", "false")
	}
	return origin, work
}

// pushCommit 在 origin 写文件并推到远程。
func pushCommit(t *testing.T, origin, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(origin, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "add", ".")
	gitRun(t, origin, "commit", "-m", message)
	gitRun(t, origin, "push")
}

// hasSubject 判断提交列表是否含指定标题（LogCommits 的排序在同秒提交间
// 不稳定，测试按集合断言）。
func hasSubject(rows []git.CommitRow, subject string) bool {
	for _, r := range rows {
		if r.Subject == subject {
			return true
		}
	}
	return false
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
