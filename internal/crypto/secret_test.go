package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
)

func key(fill byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = fill
	}
	return k
}

func newCipher(t *testing.T, fill byte) *crypto.Cipher {
	t.Helper()
	c, err := crypto.NewCipher(key(fill))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newCipher(t, 0xAB)
	plain := []byte("live_123456789_abcdefghijklmnop")

	blob, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("el ciphertext contiene el plaintext en claro")
	}

	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("Decrypt = %q, quería %q", got, plain)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	c := newCipher(t, 0xAB)
	plain := []byte("mismo texto")

	a, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("dos cifrados del mismo texto salieron idénticos: el nonce no es aleatorio")
	}
}

func TestDecryptRejectsTamperedBlob(t *testing.T) {
	c := newCipher(t, 0xAB)
	blob, err := c.Encrypt([]byte("secreto"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	blob[len(blob)-1] ^= 0x01

	if _, err := c.Decrypt(blob); err == nil {
		t.Fatal("quería error al descifrar un blob manipulado")
	}
}

func TestDecryptRejectsShortBlob(t *testing.T) {
	c := newCipher(t, 0xAB)
	if _, err := c.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Fatal("quería error con un blob más corto que el nonce")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	blob, err := newCipher(t, 0xAB).Encrypt([]byte("secreto"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := newCipher(t, 0xCD).Decrypt(blob); err == nil {
		t.Fatal("quería error al descifrar con otra clave")
	}
}

func TestCheckValueRoundTrip(t *testing.T) {
	c := newCipher(t, 0xAB)
	kcv, err := c.NewCheckValue()
	if err != nil {
		t.Fatalf("NewCheckValue: %v", err)
	}
	if err := c.VerifyCheckValue(kcv); err != nil {
		t.Errorf("VerifyCheckValue con la clave correcta: %v", err)
	}
}

func TestVerifyCheckValueDetectsWrongKey(t *testing.T) {
	kcv, err := newCipher(t, 0xAB).NewCheckValue()
	if err != nil {
		t.Fatalf("NewCheckValue: %v", err)
	}

	err = newCipher(t, 0xCD).VerifyCheckValue(kcv)
	if !errors.Is(err, crypto.ErrWrongMasterKey) {
		t.Fatalf("VerifyCheckValue = %v, quería ErrWrongMasterKey", err)
	}
}
