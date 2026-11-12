//go:build !windows

package runfile

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether pid exists. Signal 0 checks without
// delivering anything; a permission error still means the process is there.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
