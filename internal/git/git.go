// Package git 封装系统 git CLI：仓库发现、状态采集、对象读取与暂存/提交。
// 行为契约：porcelain v1、
// staged/unstaged/untracked 三区、应用内路径统一 `/` 分隔。
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const gitTimeout = 2 * time.Minute

// gitPath 解析真实 git 可执行文件。scoop 等包管理器的 shim 在 GUI 应用
// （无控制台）下启动真 git 会报 ERROR_NO_DATA 并弹黑框——读 shim 同目录的
// .shim 文本拿真实路径绕过；找不到就退回 PATH 查找结果。
var gitPath = sync.OnceValue(func() string {
	p, err := exec.LookPath("git")
	if err != nil {
		return "git"
	}
	shim := strings.TrimSuffix(p, ".exe") + ".shim"
	if data, err := os.ReadFile(shim); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if path, ok := strings.CutPrefix(line, "path = "); ok {
				return strings.Trim(strings.TrimSpace(path), `"`)
			}
			if path, ok := strings.CutPrefix(line, "path="); ok {
				return strings.Trim(strings.TrimSpace(path), `"`)
			}
		}
	}
	return p
})

// Run 执行 git 命令返回 stdout；非零退出与超时都返回错误，错误信息带 stderr 前 3 个参数。
// core.quotePath=false 让非 ASCII 路径按 UTF-8 原样返回，不转八进制转义。
func Run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitPath(), append([]string{"-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = dir
	// GUI 没有可交互终端：禁止 git 等待终端输入（凭据提示会一直挂到超时）；
	// 已配置的 credential helper 不受影响。
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	hideConsole(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) || ctx.Err() != nil {
		head := args
		if len(head) > 3 {
			head = head[:3]
		}
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			// 普通非零退出时 ctx 尚未超时（Err 为 nil），回退到原始错误
			if ctxErr := ctx.Err(); ctxErr != nil {
				reason = ctxErr.Error()
			} else {
				reason = err.Error()
			}
		}
		return "", fmt.Errorf("git %s — %s", strings.Join(head, " "), reason)
	}
	return "", err
}

// ShowObject 读取 git 对象内容（rev 如 HEAD、:0=index）。
// 对象在该 rev 不存在时 found=false；读取失败的其它错误原样返回。
func ShowObject(repoPath, rev, path string) (string, bool, error) {
	// cat-file -e 静默探测存在性：退出码非零即对象不存在，避免解析 show 的错误文本
	if _, err := Run(repoPath, "cat-file", "-e", rev+":"+path); err != nil {
		return "", false, nil
	}
	out, err := Run(repoPath, "show", rev+":"+path)
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

// IsBinaryDiff 该文件的 diff 是否为二进制：numstat 的增删列是 "-"。
func IsBinaryDiff(repoPath, path string, staged bool) (bool, error) {
	args := []string{"diff", "--numstat", "--", path}
	if staged {
		args = []string{"diff", "--cached", "--numstat", "--", path}
	}
	out, err := Run(repoPath, args...)
	if err != nil {
		return false, err
	}
	lines := strings.Split(out, "\n")
	return len(lines) > 0 && strings.HasPrefix(lines[0], "-\t"), nil
}

// ReadWorktreeFile 读工作区文件；不存在（已删除）时 found=false。
func ReadWorktreeFile(repoPath, path string) (string, bool, error) {
	data, err := os.ReadFile(repoPath + "/" + path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

// WriteWorktreeFile 写工作区文件（逐 hunk 还原的落盘通道）。
func WriteWorktreeFile(repoPath, path, content string) error {
	return os.WriteFile(repoPath+"/"+path, []byte(content), 0o644)
}

// RemoveWorktreeFile 删工作区文件（还原未跟踪文件即删除）；不存在视为已删。
func RemoveWorktreeFile(repoPath, path string) error {
	err := os.Remove(repoPath + "/" + path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeMu 序列化本进程内的 git 写操作：index 是仓库级全局资源，
// 并发 add/reset/commit/revert 会互踩 index.lock——尤其 watcher 触发的
// status（opportunistic 更新 index）与用户点击的暂存并发时。
var writeMu sync.Mutex

// runWriteGit 执行写命令：写互斥 + index.lock 撞锁退避重试。
// 撞锁时写操作未生效（ref 更新在 index 写之后），重放是安全的。
func runWriteGit(dir string, args ...string) (string, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	for attempt := 0; ; attempt++ {
		out, err := Run(dir, args...)
		if err == nil || attempt >= 2 || !strings.Contains(err.Error(), "index.lock") {
			return out, err
		}
		// 外部 git 进程持锁：退避后重试
		time.Sleep(time.Duration(200*(attempt+1)) * time.Millisecond)
	}
}

// StageFiles 文件级暂存。
func StageFiles(repoPath string, paths []string) error {
	_, err := runWriteGit(repoPath, append([]string{"add", "--"}, paths...)...)
	return err
}

// UnstageFiles 取消暂存（index 回到 HEAD，工作区不动）。
func UnstageFiles(repoPath string, paths []string) error {
	_, err := runWriteGit(repoPath, append([]string{"reset", "--"}, paths...)...)
	return err
}

// CommitRepo 提交暂存区；无内容或身份未配置时 git 非零退出，由调用方展示。
func CommitRepo(repoPath, message string) error {
	_, err := runWriteGit(repoPath, "commit", "-m", message)
	return err
}
