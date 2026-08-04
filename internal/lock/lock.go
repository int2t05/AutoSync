// lock.go 提供单实例锁，防止间隔内两个同步实例并发执行破坏仓库。
// 锁文件用 O_CREATE|O_EXCL 创建并写入 PID；已存在时检测持有进程是否存活，
// 存活则跳过本次，已死或损坏则接管。跨平台的 pidAlive 见 pidalive_*.go。
package lock

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Locker 单实例锁。
type Locker struct {
	path string
}

// New 创建锁器。
func New(path string) *Locker {
	return &Locker{path: path}
}

// Acquire 尝试获取锁。
// 返回 (是否获取, 释放函数)。若被存活进程持有 → (false, nil)，调用方应静默跳过本次。
// 若持有进程已死或锁文件损坏 → 接管。并发竞争失败时也返回 (false, nil)。
func (l *Locker) Acquire() (bool, func()) {
	if l.tryCreate() {
		return true, l.release
	}
	// 锁文件已存在：判断持有进程是否存活
	pid, ok := l.readPID()
	if ok && pidAlive(pid) {
		return false, nil // 存活，跳过本次
	}
	// 已死或损坏：删除后重建（接管）
	os.Remove(l.path)
	if l.tryCreate() {
		return true, l.release
	}
	return false, nil // 并发竞争，放弃
}

// tryCreate 用 O_EXCL 创建锁文件并写入当前 PID，成功返回 true。
func (l *Locker) tryCreate() bool {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return false
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Close()
	return true
}

// readPID 读取锁文件中的 PID。
func (l *Locker) readPID() (int, bool) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// release 释放锁（删除锁文件）。
func (l *Locker) release() {
	os.Remove(l.path)
}
