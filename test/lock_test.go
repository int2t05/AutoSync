// lock_test.go 验证单实例锁：互斥跳过、陈旧锁接管、PID 复用识别（真实文件，无 mock）。
package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestLock_SameProcess_Skips 验证持有进程存活且启动时间一致时，第二次获取跳过。
func TestLock_SameProcess_Skips(t *testing.T) {
	path := filepath.Join(makeTempDir(t, "autosync-lock-*"), "autosync.lock")

	acq1, rel1 := lock.New(path).Acquire()
	if !acq1 {
		t.Fatal("首次获取应成功")
	}
	t.Cleanup(rel1)

	// 锁内容未改动（同一进程持有时）→ 跳过
	if acq2, _ := lock.New(path).Acquire(); acq2 {
		t.Error("同进程身份一致时应跳过")
	}
}

// TestLock_PIDReuse_Takeover 验证 PID 被复用（启动时间不符）时接管锁，而非永久跳过。
func TestLock_PIDReuse_Takeover(t *testing.T) {
	path := filepath.Join(makeTempDir(t, "autosync-lock-*"), "autosync.lock")

	acq1, rel1 := lock.New(path).Acquire()
	if !acq1 {
		t.Fatal("首次获取应成功")
	}
	defer rel1()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("锁文件应含 PID + 启动时间: %q", data)
	}
	start, err := time.Parse(time.RFC3339Nano, lines[1])
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 PID 复用：同一 PID、启动时间改为 1 小时前
	reused := fmt.Sprintf("%s\n%s\n", lines[0], start.Add(-time.Hour).Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(reused), 0644); err != nil {
		t.Fatal(err)
	}

	acq2, rel2 := lock.New(path).Acquire()
	if !acq2 {
		t.Fatal("PID 复用（启动时间不符）应接管锁")
	}
	rel2()
}

// TestLock_CleanStale 验证 CleanStale：陈旧锁被清除，存活持有者的锁被保留。
func TestLock_CleanStale(t *testing.T) {
	path := filepath.Join(makeTempDir(t, "autosync-lock-*"), "autosync.lock")

	// 陈旧锁（死 PID）→ 清除
	if err := os.WriteFile(path, []byte("999999\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lock.CleanStale(path)
	if _, err := os.Stat(path); err == nil {
		t.Error("陈旧锁应被清理")
	}

	// 存活持有者（当前进程）→ 保留
	acq, rel := lock.New(path).Acquire()
	if !acq {
		t.Fatal("获取锁失败")
	}
	defer rel()
	lock.CleanStale(path)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("存活持有者的锁不应被清理: %v", err)
	}
}
