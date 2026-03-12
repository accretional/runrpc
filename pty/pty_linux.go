package pty

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Open allocates a new PTY pair via /dev/ptmx (Linux).
func Open() (master *os.File, slavePath string, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}

	// unlockpt
	var unlock int
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		master.Fd(),
		syscall.TIOCSPTLCK,
		uintptr(unsafe.Pointer(&unlock)),
	); errno != 0 {
		master.Close()
		return nil, "", fmt.Errorf("unlockpt: %w", errno)
	}

	// ptsname
	var ptyNum uint32
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		master.Fd(),
		syscall.TIOCGPTN,
		uintptr(unsafe.Pointer(&ptyNum)),
	); errno != 0 {
		master.Close()
		return nil, "", fmt.Errorf("ptsname: %w", errno)
	}

	return master, fmt.Sprintf("/dev/pts/%d", ptyNum), nil
}

// SetSize sets the window size on a PTY fd (Linux).
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

// GetSize reads the current window size from a terminal fd (Linux).
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
