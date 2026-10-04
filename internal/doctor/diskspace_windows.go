//go:build windows

package doctor

import (
	"fmt"
	"syscall"
	"unsafe"
)

var procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// queryFreeBytes returns the free space available to an unprivileged caller for
// the filesystem containing path, via GetDiskFreeSpaceExW.
func queryFreeBytes(path string) (uint64, error) {
	dir, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var freeToCaller, totalBytes, totalFreeBytes uint64
	ret, _, callErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(dir)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceExW failed for %s: %w", path, callErr)
	}
	return freeToCaller, nil
}
