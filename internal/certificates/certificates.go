// Package certificates manages the private CA and IP-address TLS certificate.
package certificates

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Info contains certificate paths and SHA-256 fingerprints.
type Info struct {
	CACertPath        string    `json:"-"`
	CAKeyPath         string    `json:"-"`
	ServerCertPath    string    `json:"-"`
	ServerKeyPath     string    `json:"-"`
	CAFingerprint     string    `json:"ca_fingerprint"`
	ServerFingerprint string    `json:"server_fingerprint"`
	NotAfter          time.Time `json:"not_after"`
}

// Ensure creates a CA and leaf certificate when missing.
func Ensure(dir, publicIP string) (Info, error) {
	paths := pathsFor(dir)
	if allExist(paths) {
		return inspect(paths)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Info{}, fmt.Errorf("create TLS directory: %w", err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Info{}, err
	}
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "olcrtc-panel local CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return Info{}, fmt.Errorf("create CA certificate: %w", err)
	}
	if err := writeCert(paths.CACertPath, caDER, 0o644); err != nil {
		return Info{}, err
	}
	if err := writeECKey(paths.CAKeyPath, caKey); err != nil {
		return Info{}, err
	}
	if err := createLeaf(paths, publicIP, caTemplate, caKey); err != nil {
		return Info{}, err
	}
	return inspect(paths)
}

// RegenerateServer replaces only the leaf certificate and preserves the CA.
func RegenerateServer(dir, publicIP string) (Info, error) {
	paths := pathsFor(dir)
	caDER, err := readCertDER(paths.CACertPath)
	if err != nil {
		return Info{}, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return Info{}, fmt.Errorf("parse CA certificate: %w", err)
	}
	caKey, err := readECKey(paths.CAKeyPath)
	if err != nil {
		return Info{}, err
	}
	if err := createLeaf(paths, publicIP, ca, caKey); err != nil {
		return Info{}, err
	}
	return inspect(paths)
}

func createLeaf(paths Info, publicIP string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) error {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "olcrtc-panel"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(2, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	if ip := net.ParseIP(strings.TrimSpace(publicIP)); ip != nil && !ip.IsLoopback() {
		template.IPAddresses = append(template.IPAddresses, ip)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, template, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create server certificate: %w", err)
	}
	if err := writeCert(paths.ServerCertPath, leafDER, 0o644); err != nil {
		return err
	}
	return writeECKey(paths.ServerKeyPath, leafKey)
}

func pathsFor(dir string) Info {
	return Info{CACertPath: filepath.Join(dir, "ca.crt"), CAKeyPath: filepath.Join(dir, "ca.key"), ServerCertPath: filepath.Join(dir, "server.crt"), ServerKeyPath: filepath.Join(dir, "server.key")}
}

func allExist(info Info) bool {
	for _, path := range []string{info.CACertPath, info.CAKeyPath, info.ServerCertPath, info.ServerKeyPath} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func inspect(info Info) (Info, error) {
	caDER, err := readCertDER(info.CACertPath)
	if err != nil {
		return Info{}, err
	}
	serverDER, err := readCertDER(info.ServerCertPath)
	if err != nil {
		return Info{}, err
	}
	server, err := x509.ParseCertificate(serverDER)
	if err != nil {
		return Info{}, fmt.Errorf("parse server certificate: %w", err)
	}
	info.CAFingerprint = fingerprint(caDER)
	info.ServerFingerprint = fingerprint(serverDER)
	info.NotAfter = server.NotAfter
	return info, nil
}

func readCertDER(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read certificate: %w", err)
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("invalid PEM certificate")
	}
	return block.Bytes, nil
}

func readECKey(path string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA key: %w", err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("invalid CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}
	return key, nil
}

func writeCert(path string, der []byte, mode os.FileMode) error {
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return atomicWrite(path, b, mode)
}

func writeECKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	b := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return atomicWrite(path, b, 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cert-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		return os.Rename(tmp, path)
	}
	return nil
}

func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return value
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	raw := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		parts = append(parts, raw[i:i+2])
	}
	return strings.Join(parts, ":")
}
