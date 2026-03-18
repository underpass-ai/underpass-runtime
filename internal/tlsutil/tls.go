package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Mode represents the TLS operation mode.
type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeServer   Mode = "server"
	ModeMutual   Mode = "mutual"
)

// ParseMode normalises a raw string into a Mode. Empty string maps to disabled.
// Accepts kernel aliases: plaintext→disabled, tls→server, mtls→mutual.
func ParseMode(raw string) (Mode, error) {
	switch raw {
	case "", "disabled", "plaintext":
		return ModeDisabled, nil
	case "server", "tls":
		return ModeServer, nil
	case "mutual", "mtls":
		return ModeMutual, nil
	default:
		return "", fmt.Errorf("unsupported TLS mode: %q (must be disabled|server|mutual or plaintext|tls|mtls)", raw)
	}
}

// Config carries paths and mode for building a *tls.Config.
type Config struct {
	Mode     Mode
	CertPath string // path to PEM-encoded certificate
	KeyPath  string // path to PEM-encoded private key
	CAPath   string // path to PEM-encoded CA certificate(s)
}

// BuildServerTLSConfig creates a *tls.Config suitable for an HTTP/gRPC server.
//
//   - disabled → (nil, nil)
//   - server   → cert+key loaded; no client auth
//   - mutual   → cert+key loaded; client CA loaded, RequireAndVerifyClientCert
func BuildServerTLSConfig(cfg Config) (*tls.Config, error) {
	if cfg.Mode == ModeDisabled {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("load server cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
	}

	if cfg.Mode == ModeMutual {
		pool, poolErr := loadCAPool(cfg.CAPath)
		if poolErr != nil {
			return nil, poolErr
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
}

// BuildClientTLSConfig creates a *tls.Config suitable for outgoing connections
// (Valkey, NATS, S3/MinIO, OTLP).
//
//   - disabled → (nil, nil)
//   - server   → custom CA in RootCAs (server-cert verification only)
//   - mutual   → custom CA + client cert+key
func BuildClientTLSConfig(cfg Config) (*tls.Config, error) {
	if cfg.Mode == ModeDisabled {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	if cfg.CAPath != "" {
		pool, err := loadCAPool(cfg.CAPath)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}

	if cfg.Mode == ModeMutual {
		if cfg.CertPath == "" || cfg.KeyPath == "" {
			return nil, fmt.Errorf("mutual TLS requires both cert and key paths")
		}
		cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}

// BuildClientTLSFromCA is a convenience for transports that only need a custom
// CA (e.g., S3, OTLP). It returns nil when caPath is empty.
func BuildClientTLSFromCA(caPath string) (*tls.Config, error) {
	if caPath == "" {
		return nil, nil
	}
	pool, err := loadCAPool(caPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
	}, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA file %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("no valid certificates in CA file %s", path)
	}
	return pool, nil
}
