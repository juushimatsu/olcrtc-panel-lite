package certificates

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"testing"
)

func TestEnsureAndRegeneratePreservesCA(t *testing.T) {
	dir := t.TempDir()
	first, err := Ensure(dir, "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	caFingerprint := first.CAFingerprint
	second, err := RegenerateServer(dir, "203.0.113.11")
	if err != nil {
		t.Fatal(err)
	}
	if second.CAFingerprint != caFingerprint {
		t.Fatal("CA changed during leaf regeneration")
	}
	b, err := os.ReadFile(second.ServerCertPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.ParseIP("203.0.113.11")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("new IP missing from SAN: %v", cert.IPAddresses)
	}
}
