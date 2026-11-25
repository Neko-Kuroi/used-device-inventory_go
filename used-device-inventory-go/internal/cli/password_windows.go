//go:build windows

package cli

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// The console-mode calls go through kernel32 directly (standard
// library only), mirroring what Python's getpass does on Windows:
// clear ENABLE_ECHO_INPUT while the password is typed.
var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle   = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

const (
	stdInputHandle  = uintptr(^uint32(9)) // STD_INPUT_HANDLE = -10
	enableEchoInput = 0x0004
)

func readPasswordFromTerminal(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	handle, _, _ := procGetStdHandle.Call(stdInputHandle)
	var mode uint32
	echoOff := false
	if ok, _, _ := procGetConsoleMode.Call(handle,
		uintptr(unsafe.Pointer(&mode))); ok != 0 {
		if ok, _, _ := procSetConsoleMode.Call(handle,
			uintptr(mode&^enableEchoInput)); ok != 0 {
			echoOff = true
		}
	}
	line, err := readPasswordLine()
	if echoOff {
		procSetConsoleMode.Call(handle, uintptr(mode))
		fmt.Fprintln(os.Stderr)
	}
	return line, err
}
