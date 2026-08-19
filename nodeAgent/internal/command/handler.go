// Package command implements the safe local actions the agent can execute on
// instructions from the control plane.
package command

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrUnknownCommand is returned for command types with no registered handler.
var ErrUnknownCommand = errors.New("unknown command")

// ErrRestartRequested is returned by handlers that requested a graceful agent
// restart. The executor treats it as a successful outcome and triggers a
// shutdown.
var ErrRestartRequested = errors.New("agent restart requested")

// Request is a command dispatched by the control plane.
type Request struct {
	ID         string
	Type       string
	Parameters map[string]any
}

// Handler executes a single command type.
type Handler interface {
	// Handle runs the command and returns its result (any JSON-serializable
	// value) or an error.
	Handle(ctx context.Context, request Request) (any, error)
}

// Registry maps command types to handlers. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty command registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register binds a handler to a command type. A blank type is ignored.
func (r *Registry) Register(commandType string, handler Handler) {
	if commandType == "" || handler == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[commandType] = handler
}

// Handle dispatches the request to its registered handler.
func (r *Registry) Handle(ctx context.Context, request Request) (any, error) {
	r.mu.RLock()
	handler, ok := r.handlers[request.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCommand, request.Type)
	}
	return handler.Handle(ctx, request)
}

// Types returns the sorted list of registered command types.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.handlers))
	for commandType := range r.handlers {
		types = append(types, commandType)
	}
	sort.Strings(types)
	return types
}

// StateSource supplies the current node state to the GET_STATUS handler.
type StateSource func() map[string]any

// GetStatusHandler answers GET_STATUS with the agent's current state. It is
// naturally idempotent and safe to retry.
type GetStatusHandler struct {
	state StateSource
}

// NewGetStatusHandler constructs a GET_STATUS handler backed by the given
// state source.
func NewGetStatusHandler(state StateSource) *GetStatusHandler {
	return &GetStatusHandler{state: state}
}

// Handle returns the current agent state snapshot.
func (h *GetStatusHandler) Handle(_ context.Context, _ Request) (any, error) {
	if h.state == nil {
		return map[string]any{}, nil
	}
	return h.state(), nil
}

// RestartHandler answers RESTART_AGENT by signalling a graceful restart. It
// has no side effects beyond returning ErrRestartRequested, so duplicate
// delivery is harmless.
type RestartHandler struct{}

// NewRestartHandler constructs a RESTART_AGENT handler.
func NewRestartHandler() *RestartHandler {
	return &RestartHandler{}
}

// Handle signals that the agent should restart itself.
func (h *RestartHandler) Handle(_ context.Context, _ Request) (any, error) {
	return map[string]any{"message": "agent restart initiated"}, ErrRestartRequested
}
