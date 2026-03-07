package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	contentTypeJSON      = "application/json"
	headerContentType    = "Content-Type"
	contentTypeMetrics   = "text/plain; version=0.0.4; charset=utf-8"
)

type Server struct {
	logger  *slog.Logger
	service *app.Service
	auth    AuthConfig
}

func NewServer(logger *slog.Logger, service *app.Service, authCfg ...AuthConfig) *Server {
	cfg := DefaultAuthConfig()
	if len(authCfg) > 0 {
		cfg = authCfg[0]
	}
	return &Server{logger: logger, service: service, auth: cfg}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSessionRoutes)
	mux.HandleFunc("/v1/invocations/", s.handleInvocationRoutes)
	return withJSONContentType(withTraceContext(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	w.Header().Set(headerContentType, contentTypeMetrics)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, s.service.PrometheusMetrics())
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var request app.CreateSessionRequest
	if err := decodeBody(r, &request); err != nil {
		writeServiceError(w, app.ErrorCodeInvalidArgument, "invalid request body", http.StatusBadRequest)
		return
	}
	if authenticatedPrincipal, authEnabled, authErr := s.resolveRequestPrincipal(r); authErr != nil {
		writeServiceError(w, authErr.code, authErr.message, authErr.status)
		return
	} else if authEnabled {
		request.Principal = authenticatedPrincipal
	}

	session, serviceErr := s.service.CreateSession(r.Context(), request)
	if serviceErr != nil {
		writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (s *Server) handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		writeServiceError(w, app.ErrorCodeInvalidArgument, "session_id is required", http.StatusBadRequest)
		return
	}

	sessionID := parts[0]
	authenticatedPrincipal, authEnabled, authErr := s.resolveRequestPrincipal(r)
	if authErr != nil {
		writeServiceError(w, authErr.code, authErr.message, authErr.status)
		return
	}

	if authEnabled {
		if serviceErr := s.service.ValidateSessionAccess(r.Context(), sessionID, authenticatedPrincipal); serviceErr != nil {
			writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
			return
		}
	}
	if len(parts) == 1 {
		s.handleSessionClose(w, r, sessionID)
		return
	}
	if len(parts) == 2 && parts[1] == "tools" {
		s.handleSessionListTools(w, r, sessionID)
		return
	}
	if len(parts) == 4 && parts[1] == "tools" && parts[3] == "invoke" {
		s.handleSessionInvokeTool(w, r, sessionID, parts[2])
		return
	}
	writeServiceError(w, app.ErrorCodeNotFound, "route not found", http.StatusNotFound)
}

func (s *Server) handleSessionClose(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	if serviceErr := s.service.CloseSession(r.Context(), sessionID); serviceErr != nil {
		writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"closed": true})
}

func (s *Server) handleSessionListTools(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	tools, serviceErr := s.service.ListTools(r.Context(), sessionID)
	if serviceErr != nil {
		writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

func (s *Server) handleSessionInvokeTool(w http.ResponseWriter, r *http.Request, sessionID, toolName string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var request app.InvokeToolRequest
	if err := decodeBody(r, &request); err != nil {
		writeServiceError(w, app.ErrorCodeInvalidArgument, "invalid request body", http.StatusBadRequest)
		return
	}
	if request.CorrelationID == "" {
		request.CorrelationID = strings.TrimSpace(r.Header.Get("X-Correlation-Id"))
	}
	invocation, serviceErr := s.service.InvokeTool(r.Context(), sessionID, toolName, request)
	if serviceErr != nil {
		writeJSON(w, serviceErr.HTTPStatus, map[string]any{
			"invocation": invocation,
			"error": map[string]any{
				"code":    serviceErr.Code,
				"message": serviceErr.Message,
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invocation": invocation})
}

func (s *Server) handleInvocationRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/invocations/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		writeServiceError(w, app.ErrorCodeInvalidArgument, "invocation_id is required", http.StatusBadRequest)
		return
	}
	invocationID := parts[0]
	authenticatedPrincipal, authEnabled, authErr := s.resolveRequestPrincipal(r)
	if authErr != nil {
		writeServiceError(w, authErr.code, authErr.message, authErr.status)
		return
	}
	if authEnabled {
		if serviceErr := s.service.ValidateInvocationAccess(r.Context(), invocationID, authenticatedPrincipal); serviceErr != nil {
			writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
			return
		}
	}

	if len(parts) == 1 {
		s.handleInvocationGet(w, r, invocationID)
		return
	}
	if len(parts) == 2 && parts[1] == "logs" {
		s.handleInvocationLogs(w, r, invocationID)
		return
	}
	if len(parts) == 2 && parts[1] == "artifacts" {
		s.handleInvocationArtifacts(w, r, invocationID)
		return
	}
	writeServiceError(w, app.ErrorCodeNotFound, "route not found", http.StatusNotFound)
}

func (s *Server) handleInvocationGet(w http.ResponseWriter, r *http.Request, invocationID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	invocation, serviceErr := s.service.GetInvocation(r.Context(), invocationID)
	if serviceErr != nil {
		writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invocation": invocation})
}

func (s *Server) handleInvocationLogs(w http.ResponseWriter, r *http.Request, invocationID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	logs, serviceErr := s.service.GetInvocationLogs(r.Context(), invocationID)
	if serviceErr != nil {
		writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

func (s *Server) handleInvocationArtifacts(w http.ResponseWriter, r *http.Request, invocationID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	artifacts, serviceErr := s.service.GetInvocationArtifacts(r.Context(), invocationID)
	if serviceErr != nil {
		writeServiceError(w, serviceErr.Code, serviceErr.Message, serviceErr.HTTPStatus)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": artifacts})
}

func (s *Server) resolveRequestPrincipal(r *http.Request) (domain.Principal, bool, *authFailure) {
	if !s.auth.requiresAuthenticatedPrincipal() {
		return domain.Principal{}, false, nil
	}
	principal, authErr := s.auth.authenticatePrincipal(r)
	if authErr != nil {
		return domain.Principal{}, true, authErr
	}
	return principal, true, nil
}

func writeServiceError(w http.ResponseWriter, code, message string, status int) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeBody(r *http.Request, destination any) error {
	if r.Body == nil {
		return errors.New("missing request body")
	}
	defer r.Body.Close()

	limited := io.LimitReader(r.Body, 2*1024*1024)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter) {
	writeServiceError(w, "method_not_allowed", "method not allowed", http.StatusMethodNotAllowed)
}

func withJSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			if r.Header.Get(headerContentType) == "" {
				r.Header.Set(headerContentType, contentTypeJSON)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func withTraceContext(next http.Handler) http.Handler {
	propagator := otel.GetTextMapPropagator()
	if propagator == nil {
		propagator = propagation.TraceContext{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
