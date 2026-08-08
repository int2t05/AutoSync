// gitignore.go 负责 .gitignore 文件的自动维护。
// 仅从文件末尾追加缺失条目，绝不覆盖已有内容；文件不存在则创建。
// 这样可避免把同步工具产物与系统垃圾文件纳入 git 跟踪，同时保留用户既有配置。
package gitignore

import (
	"bufio"
	"os"
	"strings"
)

// Ensure 确保 path 指定的 .gitignore 包含 entries 中的全部条目。
// 已存在的条目跳过；缺失的条目从文件末尾一次性追加（文件不存在则先创建）。
// 返回实际追加的条目数与可能的 error。
func Ensure(path string, entries []string) (int, error) {
	// 同一句柄内读→算→追加，闭合"读快照→追加"的 TOCTOU 窗口（TaskRunner 任务锁已串行化
	// 同一仓库访问，此处为并发追加的兜底）。不写临时文件再改名——临时文件落在仓库目录会被 git add 污染同步。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	existing, err := readExisting(f)
	if err != nil {
		return 0, err
	}

	// 收集需追加的条目，去空白、去重。按原文精确比对：`!foo` 与 `foo` 是不同的 gitignore
	// 模式，二者都需保留（git 按"后匹配覆盖先匹配"处理优先级，追加在末尾的否定条目生效）。
	var toAdd []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !existing[e] {
			toAdd = append(toAdd, e)
			existing[e] = true // 防止 entries 内自身重复
		}
	}
	if len(toAdd) == 0 {
		return 0, nil
	}

	// 一次性追加全部缺失条目（单次 Write，避免逐条写入崩溃残留半行）
	if _, err := f.WriteString(strings.Join(toAdd, "\n") + "\n"); err != nil {
		return 0, err
	}
	return len(toAdd), nil
}

// readExisting 读取句柄的 .gitignore 已有条目（从文件头扫描），去除空白后存入集合。
// 空文件视为空集合。读取后把写偏移移回末尾，配合调用方的 O_APPEND 追加。
func readExisting(f *os.File) (map[string]bool, error) {
	set := make(map[string]bool)
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			set[line] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return set, nil
}
