//go:build !windows

package main

import (
	"fmt"
	"io"
)

func dispatchServiceCommand(_ []string, _ io.Writer, stderr io.Writer) int {
	fmt.Fprintln(stderr, "Windows service commands are only available on Windows.")
	return 1
}
