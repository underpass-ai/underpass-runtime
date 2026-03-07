package tools

import (
	"context"
	"encoding/json"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type Handler interface {
	Name() string
	Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error)
}

type Engine struct {
	handlers map[string]Handler
}

func NewEngine(handlers ...Handler) *Engine {
	indexed := make(map[string]Handler, len(handlers))
	for _, handler := range handlers {
		indexed[handler.Name()] = handler
	}
	return &Engine{handlers: indexed}
}

func (e *Engine) Invoke(ctx context.Context, session domain.Session, capability domain.Capability, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	handler, ok := e.handlers[capability.Name]
	if !ok {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeNotFound,
			Message:   "tool handler not found",
			Retryable: false,
		}
	}
	return handler.Invoke(ctx, session, args)
}

type Catalog struct {
	entries map[string]domain.Capability
}

func NewCatalog(capabilities []domain.Capability) *Catalog {
	indexed := make(map[string]domain.Capability, len(capabilities))
	for _, capability := range capabilities {
		indexed[capability.Name] = capability
	}
	return &Catalog{entries: indexed}
}

func (c *Catalog) Get(name string) (domain.Capability, bool) {
	entry, ok := c.entries[name]
	return entry, ok
}

func (c *Catalog) List() []domain.Capability {
	out := make([]domain.Capability, 0, len(c.entries))
	for _, capability := range c.entries {
		out = append(out, capability)
	}
	return out
}
