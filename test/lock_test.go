// lock_test.go 验证单实例锁：互斥跳过与陈旧锁接管（真实文件，无 mock）。
package tests

import (
	"os"
	"path/filepath"
	"testing"

	"autosync/internal/lock"
)

// TestLock_SecondAcquireSkips 验证锁被存活进程持有时，第二次获取返回 false（跳过）。
func TestLock_SecondAcquireSkips(t *testing.T) {
	path := filepath.Join(makeTempDir(t, "autosync-lock-*"), "autosync.lock")

	acq1, rel1 := lock.New(path).Acquire()
	if !acq1 {
		t.Fatal("首次获取应成功")
	}
	t.Cleanup(rel1)

	// 同路径再次获取：锁文件存在且持有进程（自身）存活 → 跳过
	if acq2, _ := lock.New(path).Acquire(); acq2 {
		t.Errorf("已持有时第二次获取应失败（跳过）")
	}

	// 释放后可再次获取
	rel1()
	if acq3, rel3 := lock.New(path).Acquire(); !acq3 {
		t.Errorf("释放后应可重新获取")
	} else {
		rel3()
	}
}

// TestLock_StaleTakeover 验证锁文件中 PID 对应进程已死时，新实例接管锁。
// 999999 几乎不可能是存活进程，触发 readPID → pidAlive=false → 删除重建。
func TestLock_StaleTakeover(t *testing.T) {
	path := filepath.Join(makeTempDir(t, "autosync-lock-*"), "autosync.lock")
	if err := os.WriteFile(path, []byte("999999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	acq, rel := lock.New(path).Acquire()
	if !acq {
		t.Fatal("持有进程已死时应接管锁")
	}
	rel()
}

// TestLock_GarbledFile 验证锁文件内容损坏（非数字）时，按已死处理并接管。
func TestLock_GarbledFile(t *testing.T) {
	path := filepath.Join(makeTempDir(t, "autosync-lock-*"), "autosync.lock")
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatal(err)
	}

	acq, rel := lock.New(path).Acquire()
	if !acq {
		t.Fatal("锁文件损坏时应接管锁")
	}
	rel()
}
