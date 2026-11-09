//go:build !windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// readPasswordNoEcho disables echo on the controlling terminal via termios.
func readPasswordNoEcho() (string, error) {
	fd := int(os.Stdin.Fd())

	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(ioctlReadTermios), uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno != 0 {
		// Not a terminal (piped input): just read the line.
		line, err := stdin.ReadString('\n')
		return trimNL(line), err
	}

	nw := old
	nw.Lflag &^= syscall.ECHO
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(ioctlWriteTermios), uintptr(unsafe.Pointer(&nw)), 0, 0, 0); errno != 0 {
		line, err := stdin.ReadString('\n')
		return trimNL(line), err
	}
	defer syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(ioctlWriteTermios), uintptr(unsafe.Pointer(&old)), 0, 0, 0)

	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return trimNL(line), nil
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func platformInit() {}
