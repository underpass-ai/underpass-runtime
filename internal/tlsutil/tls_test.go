package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCA generates a self-signed CA cert and writes ca.crt + ca.key to dir.
func testCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	writePEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", der)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	writePEM(t, filepath.Join(dir, "ca.key"), "EC PRIVATE KEY", keyDER)
	return cert, key
}

// testLeaf generates a leaf cert signed by the CA and writes tls.crt + tls.key.
func testLeaf(t *testing.T, dir string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, filepath.Join(dir, "tls.crt"), "CERTIFICATE", der)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	writePEM(t, filepath.Join(dir, "tls.key"), "EC PRIVATE KEY", keyDER)
}

func writePEM(t *testing.T, path, blockType string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		t.Fatal(err)
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
		err   bool
	}{
		{"", ModeDisabled, false},
		{"disabled", ModeDisabled, false},
		{"plaintext", ModeDisabled, false},
		{"server", ModeServer, false},
		{"tls", ModeServer, false},
		{"mutual", ModeMutual, false},
		{"mtls", ModeMutual, false},
		{"invalid", "", true},
	}
	for _, tc := range tests {
		got, err := ParseMode(tc.input)
		if tc.err && err == nil {
			t.Errorf("ParseMode(%q): expected error", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("ParseMode(%q): unexpected error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBuildServerTLSConfig_Disabled(t *testing.T) {
	cfg, err := BuildServerTLSConfig(Config{Mode: ModeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Fatal("expected nil config for disabled mode")
	}
}

func TestBuildServerTLSConfig_Server(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := testCA(t, dir)
	testLeaf(t, dir, ca, caKey)

	cfg, err := BuildServerTLSConfig(Config{
		Mode:     ModeServer,
		CertPath: filepath.Join(dir, "tls.crt"),
		KeyPath:  filepath.Join(dir, "tls.key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatal("expected TLS 1.3 minimum")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(cfg.Certificates))
	}
	if cfg.ClientAuth != tls.NoClientCert {
		t.Fatal("server mode should not require client certs")
	}
}

func TestBuildServerTLSConfig_Mutual(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := testCA(t, dir)
	testLeaf(t, dir, ca, caKey)

	cfg, err := BuildServerTLSConfig(Config{
		Mode:     ModeMutual,
		CertPath: filepath.Join(dir, "tls.crt"),
		KeyPath:  filepath.Join(dir, "tls.key"),
		CAPath:   filepath.Join(dir, "ca.crt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatal("mutual mode should require client certs")
	}
	if cfg.ClientCAs == nil {
		t.Fatal("expected ClientCAs pool")
	}
}

func TestBuildServerTLSConfig_BadCert(t *testing.T) {
	_, err := BuildServerTLSConfig(Config{
		Mode:     ModeServer,
		CertPath: "/nonexistent/tls.crt",
		KeyPath:  "/nonexistent/tls.key",
	})
	if err == nil {
		t.Fatal("expected error for missing cert")
	}
}

func TestBuildServerTLSConfig_MutualBadCA(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := testCA(t, dir)
	testLeaf(t, dir, ca, caKey)

	_, err := BuildServerTLSConfig(Config{
		Mode:     ModeMutual,
		CertPath: filepath.Join(dir, "tls.crt"),
		KeyPath:  filepath.Join(dir, "tls.key"),
		CAPath:   "/nonexistent/ca.crt",
	})
	if err == nil {
		t.Fatal("expected error for missing CA")
	}
}

func TestBuildClientTLSConfig_Disabled(t *testing.T) {
	cfg, err := BuildClientTLSConfig(Config{Mode: ModeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Fatal("expected nil config for disabled mode")
	}
}

func TestBuildClientTLSConfig_Server(t *testing.T) {
	dir := t.TempDir()
	testCA(t, dir)

	cfg, err := BuildClientTLSConfig(Config{
		Mode:   ModeServer,
		CAPath: filepath.Join(dir, "ca.crt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatal("expected TLS 1.3 minimum")
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs pool")
	}
	if len(cfg.Certificates) != 0 {
		t.Fatal("server mode should not have client certs")
	}
}

func TestBuildClientTLSConfig_Mutual(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := testCA(t, dir)
	testLeaf(t, dir, ca, caKey)

	cfg, err := BuildClientTLSConfig(Config{
		Mode:     ModeMutual,
		CAPath:   filepath.Join(dir, "ca.crt"),
		CertPath: filepath.Join(dir, "tls.crt"),
		KeyPath:  filepath.Join(dir, "tls.key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs pool")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 client certificate, got %d", len(cfg.Certificates))
	}
}

func TestBuildClientTLSConfig_MutualMissingPaths(t *testing.T) {
	dir := t.TempDir()
	testCA(t, dir)

	_, err := BuildClientTLSConfig(Config{
		Mode:   ModeMutual,
		CAPath: filepath.Join(dir, "ca.crt"),
	})
	if err == nil {
		t.Fatal("expected error for missing cert/key paths in mutual mode")
	}
}

func TestBuildClientTLSFromCA(t *testing.T) {
	cfg, err := BuildClientTLSFromCA("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Fatal("expected nil for empty path")
	}

	dir := t.TempDir()
	testCA(t, dir)

	cfg, err = BuildClientTLSFromCA(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs pool")
	}
}

func TestBuildClientTLSFromCA_BadFile(t *testing.T) {
	_, err := BuildClientTLSFromCA("/nonexistent/ca.crt")
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestBuildClientTLSFromCA_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.crt")
	if err := os.WriteFile(path, []byte("not a valid PEM"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := BuildClientTLSFromCA(path)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}
