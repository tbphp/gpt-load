//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// replaceExecutable 用 rename 原子替换可执行文件。新文件是新的 inode：运行中的进程继续使用旧文件，
// 也避免 macOS 原地改写已签名文件后程序被系统终止。
func replaceExecutable(target, replacement string) error {
	if err := os.Rename(replacement, target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}

// preserveExecutableOwner 让新文件沿用原程序的属主和属组。更新者与原文件属主不同（例如 sudo）时，
// 新文件默认归更新者所有；在 0750 这类权限下，服务账号重启时会失去执行权限。
func preserveExecutableOwner(file *os.File, original os.FileInfo) error {
	want, ok := original.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("read owner of %s", original.Name())
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	current, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("read owner of %s", file.Name())
	}
	if current.Uid == want.Uid && current.Gid == want.Gid {
		return nil
	}
	return file.Chown(int(want.Uid), int(want.Gid))
}

// cleanupReplacedExecutable 在非 Windows 平台无需处理：rename 已经直接替换了旧文件。
func cleanupReplacedExecutable() {}
