package mirror

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	payload, err := Encrypt(key, []byte("olcrtc://example"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Decrypt(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "olcrtc://example" {
		t.Fatalf("plain = %q", plain)
	}
	if string(payload) == string(plain) {
		t.Fatal("mirror contains plaintext")
	}
}

func TestDecryptKnownVector(t *testing.T) {
	key := make([]byte, 32)
	nonce := make([]byte, 12)
	for i := range key {
		key[i] = byte(i + 1)
	}
	for i := range nonce {
		nonce[i] = byte(20 + i)
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	sealed := aead.Seal(nil, nonce, []byte("vector payload"), []byte("olcrtc-sub-mirror:v1"))
	envelope, _ := json.Marshal(Envelope{Type: "olcrtc-sub-mirror", Version: 1, Algorithm: "AES-256-GCM", Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(sealed)})
	plain, err := Decrypt(key, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "vector payload" {
		t.Fatalf("known vector = %q", plain)
	}
}

func TestGenerateKey(t *testing.T) {
	first, _ := GenerateKey()
	second, _ := GenerateKey()
	if len(first) != 32 || string(first) == string(second) {
		t.Fatal("invalid mirror key generation")
	}
}
