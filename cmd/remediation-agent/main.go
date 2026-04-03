// remediation-agent subscribes to NATS alert events and runs remediation
// playbooks against the workspace runtime. Deploy as a long-running pod
// alongside the runtime.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/underpass-ai/underpass-runtime/internal/remediation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/v1"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
	}))

	runtimeAddr := envOrDefault("RUNTIME_ADDR", "underpass-runtime:50053")
	natsURL := envOrDefault("NATS_URL", "nats://localhost:4222")
	subject := envOrDefault("ALERT_SUBJECT", "observability.alert.>")
	tenantID := envOrDefault("TENANT_ID", "remediation")
	actorID := envOrDefault("ACTOR_ID", "remediation-agent")

	// gRPC connection to runtime
	grpcOpts := []grpc.DialOption{}
	if caPath := os.Getenv("RUNTIME_TLS_CA_PATH"); caPath != "" {
		tlsCfg, err := buildTLS(caPath, os.Getenv("RUNTIME_TLS_CERT_PATH"), os.Getenv("RUNTIME_TLS_KEY_PATH"))
		if err != nil {
			logger.Error("TLS config failed", "error", err)
			os.Exit(1)
		}
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithInsecure())
	}

	conn, err := grpc.Dial(runtimeAddr, grpcOpts...)
	if err != nil {
		logger.Error("gRPC dial failed", "addr", runtimeAddr, "error", err)
		os.Exit(1)
	}
	defer conn.Close()
	logger.Info("connected to runtime", "addr", runtimeAddr)

	client := &grpcRuntimeClient{
		session: pb.NewSessionServiceClient(conn),
		catalog: pb.NewCapabilityCatalogServiceClient(conn),
		invoke:  pb.NewInvocationServiceClient(conn),
	}

	agent := remediation.NewAgent(remediation.AgentConfig{
		Client:   client,
		TenantID: tenantID,
		ActorID:  actorID,
		Logger:   logger,
	})

	// NATS connection
	natsOpts := []nats.Option{nats.Name("remediation-agent")}
	if caPath := os.Getenv("NATS_TLS_CA_PATH"); caPath != "" {
		tlsCfg, err := buildTLS(caPath, os.Getenv("NATS_TLS_CERT_PATH"), os.Getenv("NATS_TLS_KEY_PATH"))
		if err != nil {
			logger.Error("NATS TLS failed", "error", err)
			os.Exit(1)
		}
		natsOpts = append(natsOpts, nats.Secure(tlsCfg))
	}

	nc, err := nats.Connect(natsURL, natsOpts...)
	if err != nil {
		logger.Error("NATS connect failed", "url", natsURL, "error", err)
		os.Exit(1)
	}
	defer nc.Close()
	logger.Info("connected to NATS", "url", natsURL)

	_, err = nc.Subscribe(subject, func(msg *nats.Msg) {
		result, handleErr := agent.HandleAlert(context.Background(), msg.Data)
		if handleErr != nil {
			logger.Warn("remediation failed", "error", handleErr)
			return
		}
		if result != nil {
			logger.Info("remediation run complete",
				"alert", result.AlertName,
				"outcome", result.Outcome,
				"steps", len(result.Steps),
				"duration_ms", result.Duration.Milliseconds(),
			)
		}
	})
	if err != nil {
		logger.Error("subscribe failed", "subject", subject, "error", err)
		os.Exit(1)
	}
	logger.Info("listening for alerts", "subject", subject)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")
}

// grpcRuntimeClient adapts gRPC stubs to the remediation.RuntimeClient interface.
type grpcRuntimeClient struct {
	session pb.SessionServiceClient
	catalog pb.CapabilityCatalogServiceClient
	invoke  pb.InvocationServiceClient
}

func (c *grpcRuntimeClient) CreateSession(ctx context.Context, tenantID, actorID string) (string, error) {
	resp, err := c.session.CreateSession(ctx, &pb.CreateSessionRequest{
		Principal: &pb.Principal{TenantId: tenantID, ActorId: actorID, Roles: []string{"developer"}},
	})
	if err != nil {
		return "", err
	}
	return resp.GetSession().GetId(), nil
}

func (c *grpcRuntimeClient) CloseSession(ctx context.Context, sessionID string) error {
	_, err := c.session.CloseSession(ctx, &pb.CloseSessionRequest{SessionId: sessionID})
	return err
}

func (c *grpcRuntimeClient) RecommendTools(ctx context.Context, sessionID, taskHint string, topK int) ([]string, string, error) {
	resp, err := c.catalog.RecommendTools(ctx, &pb.RecommendToolsRequest{
		SessionId: sessionID, TaskHint: taskHint, TopK: int32(topK),
	})
	if err != nil {
		return nil, "", err
	}
	tools := make([]string, len(resp.GetRecommendations()))
	for i, r := range resp.GetRecommendations() {
		tools[i] = r.GetName()
	}
	return tools, resp.GetRecommendationId(), nil
}

func (c *grpcRuntimeClient) InvokeTool(ctx context.Context, sessionID, toolName string) (remediation.InvokeResult, error) {
	args, _ := structpb.NewStruct(map[string]any{})
	resp, err := c.invoke.InvokeTool(ctx, &pb.InvokeToolRequest{
		SessionId: sessionID, ToolName: toolName, Args: args,
	})
	if err != nil {
		return remediation.InvokeResult{}, err
	}
	inv := resp.GetInvocation()
	return remediation.InvokeResult{
		Status:     inv.GetStatus().String(),
		DurationMS: inv.GetDurationMs(),
		Error:      inv.GetError().GetMessage(),
	}, nil
}

func (c *grpcRuntimeClient) AcceptRecommendation(ctx context.Context, sessionID, recID, toolID string) error {
	_, err := c.catalog.AcceptRecommendation(ctx, &pb.AcceptRecommendationRequest{
		SessionId: sessionID, RecommendationId: recID, SelectedToolId: toolID,
	})
	return err
}

func (c *grpcRuntimeClient) RejectRecommendation(ctx context.Context, sessionID, recID, reason string) error {
	_, err := c.catalog.RejectRecommendation(ctx, &pb.RejectRecommendationRequest{
		SessionId: sessionID, RecommendationId: recID, Reason: reason,
	})
	return err
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
		pool.AppendCertsFromPEM(data)
		cfg.RootCAs = pool
	}
	if certPath != "" && keyPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load cert: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// Suppress unused import warning for structpb.
var _ = json.Marshal
