//go:build !linux && !windows

package cli

import (
	"fmt"
	"os"
)

// Fallback for platforms without a terminal implementation here:
// the password is read from stdin with echo left on.
func readPasswordFromTerminal(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	return readPasswordLine()
}
