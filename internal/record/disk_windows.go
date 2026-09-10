//go:build windows

package record

import (
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// FreeSpace devuelve el espacio libre para el usuario y el total del volumen donde vive
// dir. Va por syscall y no por golang.org/x/sys/windows para no sumar una dependencia
// directa: el spec §5 las quiere deliberadamente pocas.
func FreeSpace(dir string) (free, total int64, err error) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var libreUsuario, totalBytes, libreTotal uint64
	r, _, e := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&libreUsuario)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&libreTotal)))
	if r == 0 {
		return 0, 0, e
	}
	return int64(libreUsuario), int64(totalBytes), nil
}
