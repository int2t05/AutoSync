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

// Logger 分级日志器。文件与控制台输出均受互斥锁保护，可被多 goroutine 并发调用。
type Logger struct {
	mu      sync.Mutex
	file    *os.File
	console bool
}

// New 创建日志器。logFile 为空则仅输出到控制台（受 console 控制）。
// 文件以追加模式打开；打开失败返回 error，调用方应据此终止。
func New(logFile string, console bool) (*Logger, error) {
	l := &Logger{console: console}
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("打开日志文件失败 %s: %w", logFile, err)
		}
		l.file = f
	}
	return l, nil
}

// log 写入一条日志，加锁保证并发安全。
func (l *Logger) log(level, msg string) {
	line := fmt.Sprintf("[%s] [%s] %s", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.console {
		fmt.Println(line)
	}
	if l.file != nil {
		l.file.WriteString(line + "\n")
	}
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
