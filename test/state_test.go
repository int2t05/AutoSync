// state_test.go 验证状态持久化 Store 的读写（真实 JSON 文件，无 mock）。
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"autosync/internal/state"
)

// TestStore_SaveLoad 验证状态写入后可完整回读。
func TestStore_SaveLoad(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	st := state.New(filepath.Join(d, "autosync.state.json"))

	orig := state.State{
		LastSyncAt:   time.Now(),
		LastOutcome:  "已推送",
		LastMessage:  "sync ok",
		BackupBranch: "backup/remote-20260727_143000",
	}
	if err := st.Save(orig); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got.LastOutcome != "已推送" || got.LastMessage != "sync ok" || got.BackupBranch != "backup/remote-20260727_143000" {
		t.Errorf("状态回读不一致: %+v", got)
	}
	if got.LastSyncAt.IsZero() {
		t.Errorf("LastSyncAt 不应为零值")
	}
}

// TestStore_LoadNonexistent 验证文件不存在时返回零 State 不报错（首次运行场景）。
func TestStore_LoadNonexistent(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	st := state.New(filepath.Join(d, "nope.json"))
	got, err := st.Load()
	if err != nil {
		t.Fatalf("文件不存在应返回零 State 不报错: %v", err)
	}
	if !got.LastSyncAt.IsZero() {
		t.Errorf("文件不存在应返回零 State")
	}
}

// TestStore_SaveAtomic 验证原子写不残留临时文件，且内容可回读。
func TestStore_SaveAtomic(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	st := state.New(filepath.Join(d, "autosync.state.json"))
	if err := st.Save(state.State{LastOutcome: "已推送"}); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("原子写残留临时文件: %s", e.Name())
		}
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got.LastOutcome != "已推送" {
		t.Errorf("回读不符: %+v", got)
	}
}
