// Command inventory is the Go version of used-device-inventory.
// It is a thin main: it finds the executable's directory (the base
// for instance/ and config.txt) and hands over to the cli package.
package main

import (
	"os"
	"path/filepath"

	"used-device-inventory-go/internal/cli"
)

func main() {
	baseDir := "."
	if exe, err := os.Executable(); err == nil {
		baseDir = filepath.Dir(exe)
	}
	os.Exit(cli.Run(os.Args[1:], baseDir, os.Stdout, os.Stderr))
}
