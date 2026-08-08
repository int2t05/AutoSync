// lock.go 提供单实例锁，防止间隔内两个同步实例并发执行破坏仓库。
// 锁文件用 O_CREATE|O_EXCL 创建并写入 PID + 进程启动时间：启动时间戳防 PID 复用误判——
// 进程崩溃后 PID 被无关进程复用，仅查 PID 存活会误判"仍在持有"导致任务永久跳过。
// 跨平台 pidAlive / processStartTime 见 pidalive_*.go。
// 注意：锁仅单机语义——防止同一台机器上的并发同步；多设备同步同一远程时的竞争
// 由 git 层保证（fetch+rebase 合并 / --force-with-lease 防覆盖），不做远程分布式锁。
package lock

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Locker 单实例锁。
type Locker struct {
	path string
}

// New 创建锁器。
func New(path string) *Locker {
	return &Locker{path: path}
}

// holder 锁文件记录的持有者信息。
type holder struct {
	pid   int
	start time.Time // 持有进程启动时间
}

// Acquire 尝试获取锁。
// 返回 (是否获取, 释放函数)。若被存活且身份一致的进程持有 → (false, nil)，调用方应静默跳过本次。
// 若持有进程已死、PID 被复用或锁文件损坏 → 接管。并发竞争失败时也返回 (false, nil)。
func (l *Locker) Acquire() (bool, func()) {
	if l.tryCreate() {
		return true, l.release
	}
	h, ok := l.readHolder()
	if ok && l.holderAlive(h) {
		return false, nil // 持有进程存活且身份一致，跳过本次
	}
	// 已死 / PID 复用 / 损坏：删除后重建（接管）
	os.Remove(l.path)
	if l.tryCreate() {
		return true, l.release
	}
	return false, nil // 并发竞争，放弃
}

// holderAlive 判断持有者是否仍存活且身份一致（PID 存活且启动时间相符，防 PID 复用误判）。
func (l *Locker) holderAlive(h holder) bool {
	return pidAlive(h.pid) && h.start.Equal(processStartTime(h.pid))
}

// tryCreate 用 O_EXCL 创建锁文件并写入当前 PID 与进程启动时间，成功返回 true。
// 写入失败时删除刚创建的文件并返回 false：半写锁文件会被 readHolder 判损坏而误触发接管。
func (l *Locker) tryCreate() bool {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(f, "%d\n%s\n", os.Getpid(), processStartTime(os.Getpid()).Format(time.RFC3339Nano)); err != nil {
		f.Close()
		os.Remove(l.path)
		return false
	}
	if err := f.Close(); err != nil {
		os.Remove(l.path)
		return false
	}
	return true
}

// readHolder 读取锁文件持有者信息；内容损坏（缺 PID 或启动时间）时返回 (zero, false)。
func (l *Locker) readHolder() (holder, bool) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return holder{}, false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return holder{}, false // 缺启动时间，视为损坏
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return holder{}, false
	}
	start, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(lines[1]))
	if err != nil {
		return holder{}, false
	}
	return holder{pid: pid, start: start}, true
}

// CleanStale 删除非存活持有的锁文件（供任务重命名清理旧锁）。
// 持有者存活且身份一致时保留，避免误删进行中的任务锁。
func CleanStale(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	l := &Locker{path: path}
	h, ok := l.readHolder()
	if ok && l.holderAlive(h) {
		return
	}
	os.Remove(path)
}

// release 释放锁（删除锁文件）。
// 先比对锁内持有者 PID：锁被接管（他进程持有）或损坏时不删除，防原持有者的迟到 release 误删新锁。
func (l *Locker) release() {
	h, ok := l.readHolder()
	if !ok || h.pid != os.Getpid() {
		return
	}
	os.Remove(l.path)
}
