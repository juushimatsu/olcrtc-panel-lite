package security

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("invalid password accepted")
	}
}

func TestSecretsRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	secrets, err := NewSecrets(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := secrets.Encrypt("server-only-token")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "server-only-token" {
		t.Fatal("secret was not encrypted")
	}
	plain, err := secrets.Decrypt(encrypted)
	if err != nil || plain != "server-only-token" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
}

func TestLoadOrCreateMasterKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "master.key")
	first, err := LoadOrCreateMasterKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateMasterKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || len(first) != 32 {
		t.Fatal("master key was not stable")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("master key mode = %o", info.Mode().Perm())
	}
}

func TestTokenHash(t *testing.T) {
	if !EqualToken("csrf-token", HashToken("csrf-token")) {
		t.Fatal("matching token rejected")
	}
	if EqualToken("other", HashToken("csrf-token")) {
		t.Fatal("different token accepted")
	}
}
