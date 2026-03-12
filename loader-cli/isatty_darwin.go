package main

import (
	"syscall"
	"unsafe"
)

func isattyFd(fd int) bool {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		syscall.TIOCGETA, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}
