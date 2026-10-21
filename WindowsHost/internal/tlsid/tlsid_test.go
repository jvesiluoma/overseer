package tlsid

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadOrCreateIsStableAndRenews(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	a, err := LoadOrCreate(dir, "Test", []string{"localhost"}, []string{"192.168.137.1", "10.0.0.2"}, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir, "Test", nil, nil, now.Add(time.Hour))
	if err != nil || a.Fingerprint != b.Fingerprint {
		t.Fatalf("not reused: %v", err)
	}
	c, err := LoadOrCreate(dir, "Test", nil, nil, now.Add(780*24*time.Hour))
	if err != nil || c.Fingerprint == a.Fingerprint {
		t.Fatalf("not renewed near expiry: %v", err)
	}
	// Windows has no Unix permission bits (the key relies on the per-user %APPDATA% ACL there).
	if info, err := os.Stat(filepath.Join(dir, keyFile)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("key permissions: %v %v", info.Mode(), err)
	}
}

func TestCertificateShape(t *testing.T) {
	id, err := Generate("Test", []string{"localhost"}, []string{"192.168.137.1", "bogus"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(id.CertificatePEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.IsCA || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatal("not a plain server certificate")
	}
	if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "192.168.137.1" || cert.DNSNames[0] != "localhost" {
		t.Fatalf("SAN: %v %v", cert.IPAddresses, cert.DNSNames)
	}
	if days := cert.NotAfter.Sub(cert.NotBefore).Hours() / 24; days > 825 {
		t.Fatalf("valid for %.0f days; Apple rejects more than 825", days)
	}
	sum := sha256.Sum256(block.Bytes)
	if !strings.HasPrefix(id.Fingerprint, fmt.Sprintf("%02X:%02X:", sum[0], sum[1])) || len(id.Fingerprint) != 95 {
		t.Fatalf("fingerprint %s", id.Fingerprint)
	}
}

func TestParseRejectsMismatchedKey(t *testing.T) {
	a, _ := Generate("A", nil, nil, time.Now())
	b, _ := Generate("B", nil, nil, time.Now())
	if _, err := Parse(a.CertificatePEM, b.PrivateKeyPEM); err == nil {
		t.Fatal("mismatched key accepted")
	}
}
