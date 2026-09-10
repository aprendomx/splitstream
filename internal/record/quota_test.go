package record

import (
	"errors"
	"testing"
)

func TestFreeSpaceReportsPositiveNumbersForATempDir(t *testing.T) {
	free, total, err := FreeSpace(t.TempDir())
	if err != nil {
		t.Fatalf("FreeSpace: %v", err)
	}
	if free <= 0 || total <= 0 || free > total {
		t.Errorf("free=%d total=%d: no tiene sentido", free, total)
	}
}

func TestFreeSpaceFailsOnAMissingDir(t *testing.T) {
	if _, _, err := FreeSpace(t.TempDir() + "/no-existe"); err == nil {
		t.Error("quería error para un directorio que no existe")
	}
}

func TestQuotaCheckRefusesWhenUsedReachesMax(t *testing.T) {
	q := Quota{MaxBytes: 1000, UsedBytes: 900}
	if _, err := q.Check("", 100); !errors.Is(err, ErrDiskFull) {
		t.Errorf("con 900+100 de 1000 → err = %v, quería ErrDiskFull", err)
	}
	if _, err := q.Check("", 99); err != nil {
		t.Errorf("con 900+99 de 1000 → err = %v, quería nil", err)
	}
}

func TestQuotaCheckWarnsAtEightyPercent(t *testing.T) {
	q := Quota{MaxBytes: 1000, UsedBytes: 700}
	if warn, _ := q.Check("", 50); warn {
		t.Error("avisó al 75 %")
	}
	if warn, _ := q.Check("", 100); !warn {
		t.Error("no avisó al 80 %")
	}
}

func TestQuotaWithoutMaxNeverRefusesByUsage(t *testing.T) {
	q := Quota{UsedBytes: 1 << 40}
	if warn, err := q.Check("", 1<<40); warn || err != nil {
		t.Errorf("sin MaxBytes: warn=%v err=%v", warn, err)
	}
}

// El espacio libre del sistema de archivos se comprueba de verdad: con un mínimo
// absurdo, cualquier disco real está «lleno».
func TestQuotaCheckRefusesWhenTheFilesystemIsAlmostFull(t *testing.T) {
	q := Quota{MinFree: 1 << 62}
	if _, err := q.Check(t.TempDir(), 0); !errors.Is(err, ErrDiskFull) {
		t.Errorf("err = %v, quería ErrDiskFull por espacio libre", err)
	}
}

// Un directorio que no se puede consultar no bloquea la grabación: es preferible grabar
// a ciegas que no grabar por un sistema de archivos exótico.
func TestQuotaCheckIgnoresAnUnreadableDir(t *testing.T) {
	q := Quota{MinFree: 1 << 62}
	if _, err := q.Check(t.TempDir()+"/no-existe", 0); err != nil {
		t.Errorf("err = %v, quería nil", err)
	}
}
