//go:build !windows

package hubsvc

import (
	"syscall"
)

func setReuseAddrPort(fd uintptr) error {
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
	return nil
}
