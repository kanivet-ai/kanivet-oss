//go:build windows

package websocket_test

import "syscall"

func setRecvBuf(fd uintptr, size int) {
	_ = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, size)
}
