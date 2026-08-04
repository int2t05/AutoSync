// main_test.go 为集成测试设置 git 身份环境变量与隔离的数据目录，避免依赖全局配置、不污染真实 ~/.autosync/。
// 仅影响测试进程；生产环境由用户 git 配置提供身份，byproduct 写入 ~/.autosync/。
package tests

import (
	"os"
	"testing"

	"autosync/internal/config"
)

// TestMain 在所有测试前设置 git 提交身份与隔离数据目录。
// AUTOSYNC_DATA_DIR 指向临时目录，使 TaskRunner 写 state/lock 不落真实 home。
func TestMain(m *testing.M) {
	os.Setenv("GIT_AUTHOR_NAME", "AutoSyncTest")
	os.Setenv("GIT_AUTHOR_EMAIL", "test@autosync.local")
	os.Setenv("GIT_COMMITTER_NAME", "AutoSyncTest")
	os.Setenv("GIT_COMMITTER_EMAIL", "test@autosync.local")

	dataDir, err := os.MkdirTemp("", "autosync-testdata-*")
	if err != nil {
		panic("创建测试数据目录失败: " + err.Error())
	}
	os.Setenv("AUTOSYNC_DATA_DIR", dataDir)
	if err := config.EnsureUserDataDirs(); err != nil {
		panic("EnsureUserDataDirs 失败: " + err.Error())
	}

	code := m.Run()
	os.RemoveAll(dataDir)
	os.Exit(code)
}
