package record

import (
	"errors"
	"fmt"
)

// ErrDiskFull indica que no hay sitio para grabar: por la cuota de grabaciones o por el
// espacio libre del sistema de archivos. Es un centinela: el sink lo trata como cualquier
// fallo de conexión, y quien construye el sink decide si arrancar o no.
var ErrDiskFull = errors.New("no hay espacio para grabar")

const (
	// DefaultMinFree es el espacio libre mínimo del sistema de archivos. Llenar el disco
	// de un VPS tumba el relay entero, que es peor que no grabar (roadmap §6).
	DefaultMinFree int64 = 512 << 20
	// DefaultWarnAt es la fracción de la cuota a partir de la que se avisa.
	DefaultWarnAt = 0.8
)

// Quota es el presupuesto de disco de las grabaciones.
type Quota struct {
	MaxBytes  int64
	UsedBytes int64
	MinFree   int64
	WarnAt    float64
}

func (q Quota) minFree() int64 {
	if q.MinFree <= 0 {
		return DefaultMinFree
	}
	return q.MinFree
}

func (q Quota) warnAt() float64 {
	if q.WarnAt <= 0 {
		return DefaultWarnAt
	}
	return q.WarnAt
}

// Check comprueba la cuota con `extra` bytes escritos por el writer en curso. Un
// directorio que no se puede consultar no cuenta como lleno: grabar a ciegas es mejor
// que no grabar por un sistema de archivos exótico.
func (q Quota) Check(dir string, extra int64) (warn bool, err error) {
	used := q.UsedBytes + extra
	if q.MaxBytes > 0 && used >= q.MaxBytes {
		return false, fmt.Errorf("%w: %d de %d bytes usados", ErrDiskFull, used, q.MaxBytes)
	}
	if dir != "" {
		if free, _, ferr := FreeSpace(dir); ferr == nil && free < q.minFree() {
			return false, fmt.Errorf("%w: quedan %d bytes libres y el mínimo es %d", ErrDiskFull, free, q.minFree())
		}
	}
	warn = q.MaxBytes > 0 && float64(used) >= q.warnAt()*float64(q.MaxBytes)
	return warn, nil
}
