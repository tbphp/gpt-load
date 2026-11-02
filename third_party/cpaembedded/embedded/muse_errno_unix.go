//go:build !windows

package embedded

import "syscall"

const museConnectionReset = syscall.ECONNRESET
