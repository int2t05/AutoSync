// log.go 提供分级日志，支持文件与控制台双输出，并发安全。
// 日志格式：[时间] [级别] 消息；文件以追加方式写入，便于排查同步问题。
package log

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// 日志级别常量
const (
	levelInfo  = "INFO"
	levelWarn  = "WARN"
	levelError = "ERROR"
)

// maxLogSize 单文件日志大小上限（字节），超限轮转到 .1（保留一份旧日志）。
const maxLogSize = 10 << 20

// Logger 分级日志器。文件与控制台输出均受互斥锁保护，可被多 goroutine 并发调用。
type Logger struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	written int64 // 当前文件已写字节（增量跟踪，避免每次 stat）
	console bool
}

// New 创建日志器。logFile 为空则仅输出到控制台（受 console 控制）。
// 文件以追加模式打开；打开失败返回 error，调用方应据此终止。
// 上次运行已超限的日志在启动即轮转。
func New(logFile string, console bool) (*Logger, error) {
	l := &Logger{console: console}
	if logFile != "" {
		l.path = logFile
		if info, err := os.Stat(logFile); err == nil && info.Size() > maxLogSize {
			os.Remove(logFile + ".1")
			os.Rename(logFile, logFile+".1")
		}
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("打开日志文件失败 %s: %w", logFile, err)
		}
		if info, err := f.Stat(); err == nil {
			l.written = info.Size()
		}
		l.file = f
	}
	return l, nil
}

// log 写入一条日志，加锁保证并发安全。控制台输出走 stderr（不污染 stdout 的程序输出）；
// 文件写失败（磁盘满等）时尽力落到 stderr，不静默丢弃。
func (l *Logger) log(level, msg string) {
	line := fmt.Sprintf("[%s] [%s] %s", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.console {
		fmt.Fprintln(os.Stderr, line)
	}
	if l.file != nil {
		if l.written+int64(len(line))+1 > maxLogSize {
			l.rotateLocked()
		}
		if _, err := l.file.WriteString(line + "\n"); err != nil {
			fmt.Fprintln(os.Stderr, line)
		} else {
			l.written += int64(len(line)) + 1
		}
	}
}

// rotateLocked 轮转日志：当前文件改名 .1（覆盖旧 .1），重开新文件（须持 l.mu）。
// rename 失败（极少）时原路径仍在，重开后继续写，日志不中断，仅可能短暂超限。
func (l *Logger) rotateLocked() {
	l.file.Close()
	os.Remove(l.path + ".1")
	os.Rename(l.path, l.path+".1")
	l.file, _ = os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	l.written = 0
}

// SetConsole 动态切换控制台输出（供 CLI sync 按任务的 ShowConsole 配置决定是否打印）。
func (l *Logger) SetConsole(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.console = on
}

// Info 记录信息级日志（常规同步流程）。
func (l *Logger) Info(msg string) { l.log(levelInfo, msg) }

// Warn 记录警告级日志（如分叉、rebase 冲突、非致命异常）。
func (l *Logger) Warn(msg string) { l.log(levelWarn, msg) }

// Error 记录错误级日志（同步失败、需关注）。
func (l *Logger) Error(msg string) { l.log(levelError, msg) }

// Infof 按格式化字符串记录信息级日志。
func (l *Logger) Infof(format string, args ...any) { l.log(levelInfo, fmt.Sprintf(format, args...)) }

// Warnf 按格式化字符串记录警告级日志。
func (l *Logger) Warnf(format string, args ...any) { l.log(levelWarn, fmt.Sprintf(format, args...)) }

// Errorf 按格式化字符串记录错误级日志。
func (l *Logger) Errorf(format string, args ...any) { l.log(levelError, fmt.Sprintf(format, args...)) }

// Close 释放日志文件资源。重复调用安全。
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
