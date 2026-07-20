package mirror

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
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
	sealed := aead.Seal(nil, nonce, []byte("vector payload"), nil)
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

func TestUploadCreatesDirectoriesAndPublishes(t *testing.T) {
	var mu sync.Mutex
	created := make([]string, 0)
	uploaded := false
	published := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/resources":
			mu.Lock()
			created = append(created, r.URL.Query().Get("path"))
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/resources/upload":
			_ = json.NewEncoder(w).Encode(map[string]string{"href": server.URL + "/upload"})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			uploaded = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && r.URL.Path == "/resources/publish":
			published = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/resources":
			_ = json.NewEncoder(w).Encode(map[string]string{"public_url": "https://yadi.sk/d/test"})
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := NewClient("token", "/olcrtc/subscriptions")
	client.BaseURL = server.URL
	client.HTTP = server.Client()
	publicURL, err := client.Upload(context.Background(), "slug", []byte(`{"encrypted":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if publicURL != "https://yadi.sk/d/test" || !uploaded || !published {
		t.Fatalf("url=%q uploaded=%v published=%v", publicURL, uploaded, published)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/olcrtc", "/olcrtc/subscriptions"}
	if len(created) != len(want) || created[0] != want[0] || created[1] != want[1] {
		t.Fatalf("created directories = %#v", created)
	}
}

func TestDeleteTreatsNotFoundAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if got, _ := url.QueryUnescape(r.URL.Query().Get("path")); got != "/olcrtc/subscriptions/missing.json" {
			t.Fatalf("path = %q", got)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := NewClient("token", "/olcrtc/subscriptions")
	client.BaseURL = server.URL
	client.HTTP = server.Client()
	if err := client.Delete(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
}
