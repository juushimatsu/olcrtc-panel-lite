// Package mirror encrypts and publishes the optional Yandex Disk compatibility mirror.
package mirror

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Envelope is the public ciphertext format consumed by compatible clients.
type Envelope struct {
	Type       string `json:"type"`
	Version    int    `json:"v"`
	Algorithm  string `json:"alg"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// GenerateKey returns a per-subscription 32-byte mirror key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate mirror key: %w", err)
	}
	return key, nil
}

// Encrypt protects a legacy subscription with AES-256-GCM.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	aead, err := aeadForKey(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("mirror nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte("olcrtc-sub-mirror:v1"))
	envelope := Envelope{Type: "olcrtc-sub-mirror", Version: 1, Algorithm: "AES-256-GCM", Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}
	return json.Marshal(envelope)
}

// Decrypt is used by tests and recovery tooling.
func Decrypt(key, encoded []byte) ([]byte, error) {
	var envelope Envelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return nil, fmt.Errorf("decode mirror envelope: %w", err)
	}
	if envelope.Type != "olcrtc-sub-mirror" || envelope.Version != 1 || envelope.Algorithm != "AES-256-GCM" {
		return nil, errors.New("unsupported mirror envelope")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, errors.New("invalid mirror nonce")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid mirror ciphertext")
	}
	aead, err := aeadForKey(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, []byte("olcrtc-sub-mirror:v1"))
	if err != nil {
		return nil, errors.New("decrypt mirror")
	}
	return plain, nil
}

func aeadForKey(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("mirror key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Client implements the fixed-host Yandex Disk REST workflow.
type Client struct {
	HTTP     *http.Client
	BaseURL  string
	Token    string
	BasePath string
}

// NewClient creates a bounded Yandex client.
func NewClient(token, basePath string) *Client {
	if basePath == "" {
		basePath = "/olcrtc/subscriptions"
	}
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, BaseURL: "https://cloud-api.yandex.net/v1/disk", Token: token, BasePath: basePath}
}

// Upload stores and publishes one encrypted JSON file.
func (c *Client) Upload(ctx context.Context, slug string, payload []byte) (string, error) {
	remotePath := path.Join(c.BasePath, slug+".json")
	params := url.Values{"path": {remotePath}, "overwrite": {"true"}}
	var link struct {
		Href string `json:"href"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, "/resources/upload?"+params.Encode(), nil, &link); err != nil {
		return "", fmt.Errorf("request Yandex upload URL: %w", err)
	}
	u, err := url.Parse(link.Href)
	host := strings.ToLower(u.Hostname())
	trustedUploadHost := host == "uploader.disk.yandex.net" || strings.HasSuffix(host, ".yandex.net") || strings.HasSuffix(host, ".yandex.ru")
	if err != nil || u.Scheme != "https" || !trustedUploadHost {
		if !strings.HasPrefix(c.BaseURL, "http://127.0.0.1") && !strings.HasPrefix(c.BaseURL, "http://localhost") {
			return "", errors.New("Yandex returned an unsafe upload URL")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, link.Href, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload Yandex mirror: %w", err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Yandex upload status %d", resp.StatusCode)
	}
	_ = c.requestJSON(ctx, http.MethodPut, "/resources/publish?"+url.Values{"path": {remotePath}}.Encode(), nil, nil)
	var metadata struct {
		PublicURL string `json:"public_url"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, "/resources?"+url.Values{"path": {remotePath}}.Encode(), nil, &metadata); err != nil {
		return "", fmt.Errorf("read Yandex mirror URL: %w", err)
	}
	if metadata.PublicURL == "" {
		return "", errors.New("Yandex mirror has no public URL")
	}
	return metadata.PublicURL, nil
}

// Delete removes a remote encrypted mirror.
func (c *Client) Delete(ctx context.Context, slug string) error {
	remotePath := path.Join(c.BasePath, slug+".json")
	return c.requestJSON(ctx, http.MethodDelete, "/resources?"+url.Values{"path": {remotePath}, "permanently": {"true"}}.Encode(), nil, nil)
}

func (c *Client) requestJSON(ctx context.Context, method, suffix string, body io.Reader, output any) error {
	if c.Token == "" {
		return errors.New("Yandex OAuth token is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+suffix, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "OAuth "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if output == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output)
}
