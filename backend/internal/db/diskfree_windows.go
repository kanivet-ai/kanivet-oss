//go:build windows

package db

import (
	"path/filepath"
	"sort"

	"golang.org/x/sys/windows"
)

// freeDiskBytes reports the space available to this process on the volume
// holding path.
func freeDiskBytes(path string) (uint64, error) {
	dir, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(dir, &avail, &total, &free); err != nil {
		return 0, err
	}
	return avail, nil
}

func sortStrings(s []string) { sort.Strings(s) }
