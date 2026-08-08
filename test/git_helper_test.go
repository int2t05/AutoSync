// git_helper_test.go 提供集成测试用的真实 git 仓库夹具：临时工作仓库、裸远程、提交构造等。
// 全部基于真实 git 操作，无 mock。
package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// intPtr 返回 int 指针，供构造含 *int 字段（backup_keep/retry_count）的测试配置。
func intPtr(v int) *int { return &v }

// runGit 在 dir 中执行 git 命令，失败即终止测试；返回去空白输出。
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s 失败 (dir=%s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeTempDir 创建一个空临时目录，测试结束自动清理。
func makeTempDir(t *testing.T, prefix string) string {
	t.Helper()
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// makeBareRemote 创建一个裸仓库作为远程（默认分支 main），返回其路径。
// 必须指定 -b main，否则裸仓库 HEAD 默认指向 master，clone 时无法 checkout main。
func makeBareRemote(t *testing.T) string {
	t.Helper()
	d := makeTempDir(t, "autosync-remote-*")
	runGit(t, d, "init", "--bare", "-b", "main")
	return d
}

// makeWorkRepo 创建一个工作仓库并完成首次提交，返回其路径（未设置 remote）。
func makeWorkRepo(t *testing.T) string {
	t.Helper()
	d := makeTempDir(t, "autosync-work-*")
	runGit(t, d, "init", "-b", "main")
	writeFile(t, d, "README.md", "# init\n")
	runGit(t, d, "add", "-A")
	runGit(t, d, "commit", "-m", "init")
	return d
}

// writeFile 在 dir 下写入文件。
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// commitFile 在 repo 中写入文件并提交。
func commitFile(t *testing.T, repo, name, content string) {
	t.Helper()
	writeFile(t, repo, name, content)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "add "+name)
}

// addRemote 设置工作仓库的远程指向 bare 远程。
func addRemote(t *testing.T, repo, remote, url string) {
	t.Helper()
	runGit(t, repo, "remote", "add", remote, url)
}

// pushToRemote 推送当前分支到远程并设置上游。
func pushToRemote(t *testing.T, repo, remote, branch string) {
	t.Helper()
	runGit(t, repo, "push", "-u", remote, branch)
}

// remoteHasBranch 判断裸远程是否含指定分支。
func remoteHasBranch(t *testing.T, remote, branch string) bool {
	t.Helper()
	out, err := exec.Command("git", "ls-remote", remote, branch).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// cloneRemote 克隆裸远程到临时目录，返回路径（用于验证远程内容）。
func cloneRemote(t *testing.T, remote string) string {
	t.Helper()
	d := makeTempDir(t, "autosync-clone-*")
	if err := exec.Command("git", "clone", "-q", remote, d).Run(); err != nil {
		t.Fatalf("clone 失败: %v", err)
	}
	return d
}

// pushAuxCommitToRemote 克隆远程、写入文件提交、推送回去，使远程领先于其他仓库。
// 用于模拟"其他设备已推送新提交"的场景。
func pushAuxCommitToRemote(t *testing.T, remote, filename, content string) {
	t.Helper()
	c := cloneRemote(t, remote)
	commitFile(t, c, filename, content)
	runGit(t, c, "push", "origin", "main")
}

// fileExists 判断路径是否存在。
func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// fileContains 判断文件内容是否包含子串。
func fileContains(t *testing.T, path, want string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), want)
}

// isGitRepo 判断目录是否已是 git 仓库。
func isGitRepo(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// inRebase 判断仓库是否处于 rebase 进行中状态（存在 rebase-merge/rebase-apply 目录）。
func inRebase(t *testing.T, dir string) bool {
	t.Helper()
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", d)); err == nil {
			return true
		}
	}
	return false
}

// headShort 返回仓库 HEAD 的短哈希，用于断言提交状态。
func headShort(t *testing.T, repo string) string {
	t.Helper()
	return runGit(t, repo, "rev-parse", "--short", "HEAD")
}

// remoteHeadCount 返回裸远程 main 分支的提交数（通过临时克隆计数）。
func remoteHeadCount(t *testing.T, remote string) int {
	t.Helper()
	c := cloneRemote(t, remote)
	out := runGit(t, c, "rev-list", "--count", "HEAD")
	var n int
	fmt.Sscanf(out, "%d", &n)
	return n
}
