//go:build windows

package hubsvc

import (
	"syscall"
)

func setReuseAddrPort(fd uintptr) error {
	_ = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	return nil
}
