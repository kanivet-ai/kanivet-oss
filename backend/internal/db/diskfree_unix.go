//go:build !windows

package db

import (
	"path/filepath"
	"sort"
	"syscall"
)

// freeDiskBytes reports the space available to this process on the volume
// holding path.
func freeDiskBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func sortStrings(s []string) { sort.Strings(s) }
