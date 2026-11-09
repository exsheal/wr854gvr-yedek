//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle   = kernel32.NewProc("GetStdHandle")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
)

const (
	stdInputHandle       = 0xFFFFFFF6 // -10
	enableEchoInput      = 0x0004
	enableLineInput      = 0x0002
	enableProcessedInput = 0x0001
)

// readPasswordNoEcho reads a line from the console with echo disabled.
func readPasswordNoEcho() (string, error) {
	h, _, _ := procGetStdHandle.Call(uintptr(stdInputHandle))
	if h == 0 || h == uintptr(syscall.InvalidHandle) {
		return "", errors.New("konsol tanıtıcısı alınamadı")
	}

	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return "", errors.New("konsol modu okunamadı")
	}

	// keep line input + processed input, drop echo
	newMode := (mode &^ enableEchoInput) | enableLineInput | enableProcessedInput
	if r, _, _ := procSetConsoleMode.Call(h, uintptr(newMode)); r == 0 {
		return "", errors.New("konsol modu ayarlanamadı")
	}
	defer procSetConsoleMode.Call(h, uintptr(mode))

	line, err := stdin.ReadString('\n')
	// trim CR/LF
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// ensureStdout keeps a sane console codepage experience on Windows.
func platformInit() {
	// Enable UTF-8 output (chcp 65001 equivalent) so Turkish text prints.
	const cpUTF8 = 65001
	kernel32.NewProc("SetConsoleOutputCP").Call(cpUTF8)
	kernel32.NewProc("SetConsoleCP").Call(cpUTF8)
	_ = os.Stdout
}
