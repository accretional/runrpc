package pty

/*
#include <stdlib.h>
#include <fcntl.h>
#include <unistd.h>
*/
import "C"

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Open allocates a new PTY pair using posix_openpt (Darwin).
func Open() (master *os.File, slavePath string, err error) {
	fd, errno := C.posix_openpt(C.O_RDWR | C.O_NOCTTY)
	if fd < 0 {
		return nil, "", fmt.Errorf("posix_openpt: %v", errno)
	}

	if C.grantpt(fd) != 0 {
		C.close(fd)
		return nil, "", fmt.Errorf("grantpt failed")
	}

	if C.unlockpt(fd) != 0 {
		C.close(fd)
		return nil, "", fmt.Errorf("unlockpt failed")
	}

	name := C.ptsname(fd)
	if name == nil {
		C.close(fd)
		return nil, "", fmt.Errorf("ptsname failed")
	}
	slavePath = C.GoString(name)

	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	return master, slavePath, nil
}

// SetSize sets the window size on a PTY fd (Darwin).
func SetSize(fd uintptr, ws *Winsize) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// GetSize reads the current window size from a terminal fd (Darwin).
func GetSize(fd uintptr) (*Winsize, error) {
	var ws Winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return nil, errno
	}
	return &ws, nil
}
