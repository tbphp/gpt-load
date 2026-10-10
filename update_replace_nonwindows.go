//go:build !windows

package main

import (
	"fmt"
	"os"
)

// replaceExecutable 用 rename 原子替换可执行文件。新文件是新的 inode：运行中的进程继续使用旧文件，
// 也避免 macOS 原地改写已签名文件后程序被系统终止。
func replaceExecutable(target, replacement string) error {
	if err := os.Rename(replacement, target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}

// cleanupReplacedExecutable 在非 Windows 平台无需处理：rename 已经直接替换了旧文件。
func cleanupReplacedExecutable() {}
