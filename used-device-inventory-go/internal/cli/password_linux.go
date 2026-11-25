//go:build linux

package cli

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// ioctl requests for termios on Linux, and the ECHO flag in
// c_lflag. (The standard library's syscall package carries the
// Termios struct but not these constants on every architecture,
// so they are spelled out here.)
const (
	tcgets   = 0x5401
	tcsets   = 0x5402
	echoFlag = 0x8 // ECHO
)

// readPasswordFromTerminal turns echo off while reading, the way
// Python's getpass does. When stdin is not a terminal (a pipe, a
// test), it simply reads a line with echo left as it is.
func readPasswordFromTerminal(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := uintptr(os.Stdin.Fd())
	var old syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, tcgets,
		uintptr(unsafe.Pointer(&old))); errno != 0 {
		return readPasswordLine()
	}
	quiet := old
	quiet.Lflag &^= echoFlag
	syscall.Syscall(syscall.SYS_IOCTL, fd, tcsets,
		uintptr(unsafe.Pointer(&quiet)))
	line, err := readPasswordLine()
	syscall.Syscall(syscall.SYS_IOCTL, fd, tcsets,
		uintptr(unsafe.Pointer(&old)))
	fmt.Fprintln(os.Stderr)
	return line, err
}
