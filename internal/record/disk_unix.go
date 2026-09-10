//go:build !windows

package record

import "syscall"

// FreeSpace devuelve el espacio libre para el usuario y el total del sistema de archivos
// donde vive dir. Bavail y no Bfree: lo que root se reserva no cuenta.
func FreeSpace(dir string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	// Bsize es uint32 en darwin e int64 en linux: la conversión explícita vale en ambos.
	bs := int64(st.Bsize)
	return int64(st.Bavail) * bs, int64(st.Blocks) * bs, nil
}
