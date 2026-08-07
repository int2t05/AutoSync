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
// 已存在的条目跳过；缺失的条目从文件末尾追加（文件不存在则先创建）。
// 返回实际追加的条目数与可能的 error。
func Ensure(path string, entries []string) (int, error) {
	existing, err := readExisting(path)
	if err != nil {
		return 0, err
	}

	// 收集需追加的条目，去空白、去重
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

	// 一次性追加全部缺失条目（单次 Write，避免逐条写入崩溃残留半行）。
	// 不写临时文件再改名——临时文件落在仓库目录会被 git add 污染同步。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if _, err := f.WriteString(strings.Join(toAdd, "\n") + "\n"); err != nil {
		return 0, err
	}
	return len(toAdd), nil
}

// readExisting 读取 .gitignore 已有条目，去除空白后存入集合。
// 文件不存在视为空集合，不报错（首次运行场景）。
func readExisting(path string) (map[string]bool, error) {
	set := make(map[string]bool)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return nil, err
	}
	defer f.Close()

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
