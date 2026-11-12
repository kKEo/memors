//go:build windows

package runfile

import "os"

// processAlive reports whether pid exists. On Windows FindProcess opens a
// handle, which fails for an exited process.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
