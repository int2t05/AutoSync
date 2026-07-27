// main_test.go 为集成测试设置 git 身份环境变量，避免依赖全局 git 配置。
// 仅影响测试进程；生产环境由用户 git 配置提供身份。
package tests

import (
	"os"
	"testing"
)

// TestMain 在所有测试前设置 git 提交身份，使临时仓库的 commit 操作无需全局配置。
func TestMain(m *testing.M) {
	os.Setenv("GIT_AUTHOR_NAME", "AutoSyncTest")
	os.Setenv("GIT_AUTHOR_EMAIL", "test@autosync.local")
	os.Setenv("GIT_COMMITTER_NAME", "AutoSyncTest")
	os.Setenv("GIT_COMMITTER_EMAIL", "test@autosync.local")
	os.Exit(m.Run())
}
