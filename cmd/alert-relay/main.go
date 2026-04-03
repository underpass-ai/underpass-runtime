package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/underpass-ai/underpass-runtime/internal/alertrelay"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
	}))

	natsURL := envOrDefault("NATS_URL", "nats://localhost:4222")
	natsOpts := []nats.Option{nats.Name("alert-relay")}

	if os.Getenv("NATS_TLS_CA_PATH") != "" {
		tlsCfg, err := buildTLS(
			os.Getenv("NATS_TLS_CA_PATH"),
			os.Getenv("NATS_TLS_CERT_PATH"),
			os.Getenv("NATS_TLS_KEY_PATH"),
		)
		if err != nil {
			logger.Error("failed to build NATS TLS", "error", err)
			os.Exit(1)
		}
		natsOpts = append(natsOpts, nats.Secure(tlsCfg))
	}

	nc, err := nats.Connect(natsURL, natsOpts...)
	if err != nil {
		logger.Error("failed to connect to NATS", "url", natsURL, "error", err)
		os.Exit(1)
	}
	defer nc.Close()
	logger.Info("connected to NATS", "url", natsURL)

	handler := alertrelay.NewHandler(alertrelay.HandlerConfig{
		Publisher:     alertrelay.NewNATSPublisher(nc),
		SubjectPrefix: envOrDefault("SUBJECT_PREFIX", "observability.alert"),
		AuthToken:     os.Getenv("WEBHOOK_AUTH_TOKEN"),
		Logger:        logger,
	})

	port := envOrDefault("PORT", "8080")
	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("alert-relay listening", "port", port)
	if err := server.ListenAndServe(); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLogLevel(raw string) slog.Level {
	switch raw {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func buildTLS(caPath, certPath, keyPath string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if caPath != "" {
		data, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("invalid CA cert")
		}
		cfg.RootCAs = pool
	}
	if certPath != "" && keyPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
