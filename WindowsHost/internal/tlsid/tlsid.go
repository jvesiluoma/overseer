// Package tlsid manages the host's self-signed TLS certificate.
//
// It is created once and kept on disk so the fingerprint stays stable: browsers remember the
// exception the user accepted, and the user can compare the fingerprint shown in the tray menu.
package tlsid

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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

const (
	certificateFile = "tls-certificate.pem"
	keyFile         = "tls-key.pem"
	// Apple rejects TLS server certificates valid for more than 825 days, even self-signed ones;
	// iPads are common clients, so stay below that.
	validity = 800 * 24 * time.Hour
	// Regenerate when less than this is left, so the certificate never expires mid-session.
	renewalMargin = 30 * 24 * time.Hour
)

type Identity struct {
	CertificatePEM []byte
	PrivateKeyPEM  []byte
	// SHA-256 of the DER certificate, as colon-separated uppercase hex (what browsers display).
	Fingerprint   string
	NotValidAfter time.Time
	certificate   tls.Certificate
}

// LoadOrCreate loads the identity from dir, or creates (and saves) a new one if it is missing,
// unreadable or close to expiry. Names and addresses only matter for new certificates.
func LoadOrCreate(dir, commonName string, dnsNames, ipAddresses []string, now time.Time) (*Identity, error) {
	certPath, keyPath := filepath.Join(dir, certificateFile), filepath.Join(dir, keyFile)
	certPEM, err1 := os.ReadFile(certPath)
	keyPEM, err2 := os.ReadFile(keyPath)
	if err1 == nil && err2 == nil {
		if id, err := Parse(certPEM, keyPEM); err == nil && id.NotValidAfter.Sub(now) > renewalMargin {
			return id, nil
		}
	}
	id, err := Generate(commonName, dnsNames, ipAddresses, now)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeFile(keyPath, id.PrivateKeyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := writeFile(certPath, id.CertificatePEM, 0o644); err != nil {
		return nil, err
	}
	return id, nil
}

func Generate(commonName string, dnsNames, ipAddresses []string, now time.Time) (*Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, a := range ipAddresses {
		if ip := net.ParseIP(a); ip != nil {
			ips = append(ips, ip)
		}
	}
	name := pkix.Name{CommonName: commonName, Organization: []string{"Overscreen"}}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               name,
		Issuer:                name,
		NotBefore:             now.Add(-time.Hour), // tolerate clock skew on the device
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return Parse(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

// Parse checks that the key belongs to the certificate.
func Parse(certPEM, keyPEM []byte) (*Identity, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("no certificate")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pair.Certificate[0])
	hexes := make([]string, len(sum))
	for i, b := range sum {
		hexes[i] = fmt.Sprintf("%02X", b)
	}
	return &Identity{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM, Fingerprint: strings.Join(hexes, ":"),
		NotValidAfter: cert.NotAfter, certificate: pair}, nil
}

// TLSConfig for the HTTPS listener (TLS 1.2 or newer).
func (id *Identity) TLSConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{id.certificate}, MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"}}
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
