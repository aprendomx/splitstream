// Package crypto agrupa las primitivas de secreto de Splitstream: cifrado simétrico
// de credenciales en reposo y hash de contraseñas. No importa nada del proyecto.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
)

// ErrWrongMasterKey indica que la master key configurada no es la que cifró esta
// base de datos. Es un centinela para poder abortar el arranque con un mensaje claro.
var ErrWrongMasterKey = errors.New("la master key no corresponde a esta base de datos")

// checkValuePlaintext es la constante conocida que se cifra para formar el key check
// value. No es un secreto: su valor es público y está aquí a propósito.
const checkValuePlaintext = "splitstream-kcv-v1"

// Cipher cifra y descifra secretos con AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher construye un Cipher a partir de una master key de 32 bytes.
func NewCipher(key [32]byte) (*Cipher, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt devuelve nonce || ciphertext || tag. El nonce es aleatorio en cada llamada.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	// Seal añade al final de nonce, así que el nonce queda como prefijo del resultado.
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt deshace Encrypt. El error nunca incluye material del texto.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(blob) < n {
		return nil, errors.New("blob cifrado demasiado corto")
	}
	plaintext, err := c.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return nil, errors.New("no se pudo descifrar: clave incorrecta o dato alterado")
	}
	return plaintext, nil
}

// NewCheckValue cifra una constante conocida. Guardarlo permite detectar al arrancar
// que la master key cambió, en vez de devolver basura descifrada más tarde.
func (c *Cipher) NewCheckValue() ([]byte, error) {
	return c.Encrypt([]byte(checkValuePlaintext))
}

// VerifyCheckValue comprueba un key check value creado por NewCheckValue.
func (c *Cipher) VerifyCheckValue(kcv []byte) error {
	plaintext, err := c.Decrypt(kcv)
	if err != nil {
		return ErrWrongMasterKey
	}
	if subtle.ConstantTimeCompare(plaintext, []byte(checkValuePlaintext)) != 1 {
		return ErrWrongMasterKey
	}
	return nil
}
