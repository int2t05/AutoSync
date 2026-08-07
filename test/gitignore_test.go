// gitignore_test.go 验证 .gitignore 维护：追加缺失、不重复、不覆盖已有（真实临时文件，无 mock）。
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"autosync/internal/gitignore"
)

// TestEnsure_CreatesFile 验证文件不存在时创建并写入全部条目。
func TestEnsure_CreatesFile(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := filepath.Join(d, ".gitignore")

	added, err := gitignore.Ensure(p, []string{"*.tmp", "Thumbs.db"})
	if err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	if added != 2 {
		t.Errorf("追加数 = %d, 期望 2", added)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if !strings.Contains(s, "*.tmp") || !strings.Contains(s, "Thumbs.db") {
		t.Errorf("文件内容缺失: %s", s)
	}
}

// TestEnsure_AppendsMissing_PreservesExisting 验证仅追加缺失条目、保留已有内容。
func TestEnsure_AppendsMissing_PreservesExisting(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := filepath.Join(d, ".gitignore")
	if err := os.WriteFile(p, []byte("# my ignores\n*.tmp\n"), 0644); err != nil {
		t.Fatal(err)
	}

	added, err := gitignore.Ensure(p, []string{"*.tmp", "desktop.ini"})
	if err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	if added != 1 {
		t.Errorf("追加数 = %d, 期望 1（*.tmp 已存在）", added)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if !strings.Contains(s, "# my ignores") {
		t.Errorf("已有内容被覆盖: %s", s)
	}
	if !strings.Contains(s, "desktop.ini") {
		t.Errorf("未追加 desktop.ini: %s", s)
	}
}

// TestEnsure_NoDuplicate 验证重复调用不产生重复条目。
func TestEnsure_NoDuplicate(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := filepath.Join(d, ".gitignore")

	if _, err := gitignore.Ensure(p, []string{"*.tmp"}); err != nil {
		t.Fatal(err)
	}
	added, err := gitignore.Ensure(p, []string{"*.tmp"})
	if err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	if added != 0 {
		t.Errorf("重复追加 = %d, 期望 0", added)
	}
	data, _ := os.ReadFile(p)
	if c := strings.Count(string(data), "*.tmp"); c != 1 {
		t.Errorf("*.tmp 出现 %d 次, 期望 1", c)
	}
}

// TestEnsure_NoTempResidue 验证 Ensure 不产生临时文件残留（避免污染同步仓库目录）。
func TestEnsure_NoTempResidue(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := filepath.Join(d, ".gitignore")
	if _, err := gitignore.Ensure(p, []string{"*.tmp", "desktop.ini"}); err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	entries, _ := os.ReadDir(d)
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Errorf("Ensure 不应在仓库目录产生临时文件: %s", e.Name())
		}
	}
}
