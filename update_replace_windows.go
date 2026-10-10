//go:build windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const replacedExecutableSuffix = ".old"

// replaceExecutable 替换 Windows 可执行文件。运行中的 exe 不能覆盖或删除，但可以改名，
// 所以先把当前文件移到 .old，再把新文件移到原路径；.old 在下次启动时删除。
func replaceExecutable(target, replacement string) error {
	previous := target + replacedExecutableSuffix
	if err := os.Remove(previous); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s left by a previous update; restart GPT-Load and try again: %w", previous, err)
	}
	if err := os.Rename(target, previous); err != nil {
		return fmt.Errorf("move %s aside: %w", target, err)
	}
	if err := os.Rename(replacement, target); err != nil {
		replaceErr := fmt.Errorf("replace %s: %w", target, err)
		if restoreErr := os.Rename(previous, target); restoreErr != nil {
			return errors.Join(replaceErr, fmt.Errorf("restore %s: %w", target, restoreErr))
		}
		return replaceErr
	}
	// 通常会失败：执行更新的进程自己仍在使用旧文件，留给下次启动清理。
	_ = os.Remove(previous)
	return nil
}

// cleanupReplacedExecutable 删除上次更新移开的旧文件。旧进程仍在运行或没有删除权限时会失败，
// 留到下次启动或下次更新再删，因此忽略错误。
func cleanupReplacedExecutable() {
	executable, err := currentExecutable()
	if err != nil {
		return
	}
	_ = os.Remove(executable + replacedExecutableSuffix)
}
