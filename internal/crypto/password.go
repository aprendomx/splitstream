package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parámetros de argon2id. Cambiarlos invalida los hashes existentes: si alguna vez
// hay que subirlos, habrá que rehashear en el siguiente login correcto.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var b64 = base64.RawStdEncoding

// HashPassword devuelve la contraseña hasheada en formato PHC.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("la contraseña no puede estar vacía")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(hash),
	), nil
}

// VerifyPassword comprueba una contraseña contra un codificado de HashPassword.
// Devuelve error solo si el codificado está malformado; una contraseña equivocada
// es (false, nil).
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("hash de contraseña malformado")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, errors.New("hash de contraseña malformado: versión")
	}
	if version != argon2.Version {
		return false, fmt.Errorf("versión de argon2 no soportada: %d", version)
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errors.New("hash de contraseña malformado: parámetros")
	}

	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("hash de contraseña malformado: salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("hash de contraseña malformado: hash")
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
