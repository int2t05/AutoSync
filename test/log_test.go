// log_test.go 验证日志器的文件/控制台输出与并发安全（真实临时文件，无 mock）。
package tests

import (
	"os"
	"strings"
	"sync"
	"testing"

	"autosync/internal/log"
)

// TestLogger_WritesFile 验证 Info/Warn/Error 三级均写入文件且格式正确。
func TestLogger_WritesFile(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := d + "/autosync.log"

	lg, err := log.New(p, false)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	lg.Info("信息行")
	lg.Warn("警告行")
	lg.Error("错误行")
	lg.Close()

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读日志失败: %v", err)
	}
	s := string(data)
	for _, want := range []string{"[INFO]", "[WARN]", "[ERROR]", "信息行", "警告行", "错误行"} {
		if !strings.Contains(s, want) {
			t.Errorf("日志缺少 %q\n实际内容:\n%s", want, s)
		}
	}
}

// TestLogger_FormatMethods 验证 Infof/Warnf/Errorf 按格式化字符串写入文件。
func TestLogger_FormatMethods(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := d + "/autosync.log"

	lg, err := log.New(p, false)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	lg.Infof("信息 %d", 1)
	lg.Warnf("警告 %s", "x")
	lg.Errorf("错误 %v", os.ErrNotExist)
	lg.Close()

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读日志失败: %v", err)
	}
	s := string(data)
	for _, want := range []string{"[INFO]", "[WARN]", "[ERROR]", "信息 1", "警告 x"} {
		if !strings.Contains(s, want) {
			t.Errorf("日志缺少 %q\n实际内容:\n%s", want, s)
		}
	}
}

// TestLogger_NoFile_ConsoleOnly 验证 logFile 为空时仅控制台、不创建文件、不报错。
func TestLogger_NoFile_ConsoleOnly(t *testing.T) {
	lg, err := log.New("", true)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	lg.Info("仅控制台")
	lg.Close()
}

// TestLogger_ConcurrentSafe 验证多 goroutine 并发写入不丢数据、不 panic。
// 配合 go test -race 可进一步验证无数据竞争。
func TestLogger_ConcurrentSafe(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	lg, err := log.New(d+"/autosync.log", false)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer lg.Close()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			lg.Info("并发行")
		}()
	}
	wg.Wait()

	data, _ := os.ReadFile(d + "/autosync.log")
	if got := strings.Count(string(data), "并发行"); got != n {
		t.Errorf("并发写入丢失: 记录到 %d 行, 期望 %d", got, n)
	}
}
