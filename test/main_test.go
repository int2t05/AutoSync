// main_test.go 为集成测试设置 git 身份环境变量与隔离的数据目录，避免依赖全局配置、不污染真实 ~/.autosync/。
// 仅影响测试进程；生产环境由用户 git 配置提供身份，byproduct 写入 ~/.autosync/。
package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"autosync/internal/config"
)

// TestMain 在所有测试前设置 git 提交身份与隔离数据目录，并构建 engine 子进程测试用二进制。
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

	buildEngineBinary()

	code := m.Run()
	os.RemoveAll(dataDir)
	if engineBinPath != "" {
		os.RemoveAll(filepath.Dir(engineBinPath))
	}
	os.Exit(code)
}

// buildEngineBinary 把 autosync 构建到临时目录，供 engine 子进程测试复用；包结束清理。
// 构建失败仅记录 engineBinErr，engine 测试据此 skip（如缺 go 工具链），不影响其他测试。
func buildEngineBinary() {
	dir, err := os.MkdirTemp("", "autosync-engine-*")
	if err != nil {
		engineBinErr = err
		return
	}
	name := "autosync-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	engineBinPath = filepath.Join(dir, name)
	// go test 的 cwd 为 test/，仓库根是其父目录
	cmd := exec.Command("go", "build", "-o", engineBinPath, "./cmd/autosync")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		engineBinErr = &buildError{cmd: "go build", err: err, out: out}
	}
}

// buildError 包装构建失败信息，供 engineBin skip 时展示。
type buildError struct {
	cmd string
	err error
	out []byte
}

func (e *buildError) Error() string {
	return e.cmd + ": " + e.err.Error() + "\n" + string(e.out)
}
