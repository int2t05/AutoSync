// retry_test.go 验证指数退避重试控制流（纯函数，无 mock）。
// baseDelay=0 使 time.Sleep(0) 立即返回，测试不实际等待退避。
package tests

import (
	"errors"
	"testing"
	"time"

	"autosync/internal/gitop"
	"autosync/internal/log"
)

// TestRetry_FailsThenSucceeds 验证前两次失败、第三次成功时返回 nil 且共调用 3 次。
func TestRetry_FailsThenSucceeds(t *testing.T) {
	logger, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	calls := 0
	fn := func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	}
	if err := gitop.Retry(fn, 3, 0, logger); err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
	if calls != 3 {
		t.Errorf("调用次数 = %d, 期望 3", calls)
	}
}

// TestRetry_AllFail 验证全部失败时返回最后一次错误且耗尽重试次数。
func TestRetry_AllFail(t *testing.T) {
	logger, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	calls := 0
	want := errors.New("permanent")
	fn := func() error {
		calls++
		return want
	}
	err = gitop.Retry(fn, 3, 0, logger)
	if !errors.Is(err, want) {
		t.Fatalf("期望返回最后错误 %v, 得到 %v", want, err)
	}
	if calls != 3 {
		t.Errorf("调用次数 = %d, 期望 3", calls)
	}
}

// TestRetry_FirstSuccess 验证首次成功即返回，不触发后续重试。
func TestRetry_FirstSuccess(t *testing.T) {
	logger, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	calls := 0
	fn := func() error {
		calls++
		return nil
	}
	// 即便 baseDelay 很大，首次成功也不会 sleep
	if err := gitop.Retry(fn, 5, time.Second, logger); err != nil {
		t.Fatalf("期望成功: %v", err)
	}
	if calls != 1 {
		t.Errorf("调用次数 = %d, 期望 1", calls)
	}
}

// TestRetry_CountLessThanOne 验证 count<1 钳制为 1：只调用一次，不进入退避重试。
func TestRetry_CountLessThanOne(t *testing.T) {
	logger, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	for _, count := range []int{0, -1} {
		calls := 0
		want := errors.New("x")
		fn := func() error {
			calls++
			return want
		}
		if err := gitop.Retry(fn, count, 0, logger); !errors.Is(err, want) {
			t.Fatalf("count=%d: 期望错误 %v, 得到 %v", count, want, err)
		}
		if calls != 1 {
			t.Errorf("count=%d: 调用次数 = %d, 期望钳制为 1", count, calls)
		}
	}
}

// TestRetry_ExponentialBackoff 验证指数退避：count=3 全失败累计等待 baseDelay+2*baseDelay（30+60ms）。
// 用下界断言（sleep 后必然至少等待）：指数（90ms）与常数（60ms）可区分，且下界不受慢机器影响。
func TestRetry_ExponentialBackoff(t *testing.T) {
	logger, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	fn := func() error { return errors.New("x") }
	start := time.Now()
	gitop.Retry(fn, 3, 30*time.Millisecond, logger)
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Errorf("指数退避等待不足：elapsed=%v, 期望 >= 90ms（30ms+60ms）", elapsed)
	}
}
