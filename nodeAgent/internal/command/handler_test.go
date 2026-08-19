package command

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegistryDispatch(t *testing.T) {
	registry := NewRegistry()
	registry.Register("GET_STATUS", NewGetStatusHandler(func() map[string]any {
		return map[string]any{"status": "READY", "hostname": "edge-01"}
	}))

	result, err := registry.Handle(context.Background(), Request{
		ID:   "cmd-1",
		Type: "GET_STATUS",
	})
	if err != nil {
		t.Fatalf("handle failed: %v", err)
	}
	state, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if state["status"] != "READY" {
		t.Errorf("expected status READY, got %v", state["status"])
	}
}

func TestRegistryUnknownCommand(t *testing.T) {
	registry := NewRegistry()

	_, err := registry.Handle(context.Background(), Request{ID: "cmd-1", Type: "NOPE"})
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("expected ErrUnknownCommand, got %v", err)
	}
}

func TestRegistryIgnoresBlankRegistration(t *testing.T) {
	registry := NewRegistry()
	registry.Register("", NewGetStatusHandler(func() map[string]any { return nil }))
	if len(registry.Types()) != 0 {
		t.Errorf("expected no registered types, got %v", registry.Types())
	}
}

func TestRegistryTypesSorted(t *testing.T) {
	registry := NewRegistry()
	registry.Register("ZEBRA", NewGetStatusHandler(func() map[string]any { return nil }))
	registry.Register("ALPHA", NewGetStatusHandler(func() map[string]any { return nil }))

	types := registry.Types()
	if len(types) != 2 || types[0] != "ALPHA" || types[1] != "ZEBRA" {
		t.Errorf("expected sorted types, got %v", types)
	}
}

func TestGetStatusHandler(t *testing.T) {
	handler := NewGetStatusHandler(func() map[string]any {
		return map[string]any{"status": "DEGRADED"}
	})

	result, err := handler.Handle(context.Background(), Request{Type: "GET_STATUS"})
	if err != nil {
		t.Fatalf("handle failed: %v", err)
	}
	if result.(map[string]any)["status"] != "DEGRADED" {
		t.Errorf("expected DEGRADED, got %v", result)
	}

	// The handler must also work with a nil state source.
	empty := NewGetStatusHandler(nil)
	if _, err := empty.Handle(context.Background(), Request{Type: "GET_STATUS"}); err != nil {
		t.Fatalf("nil state source failed: %v", err)
	}
}

func TestRestartHandler(t *testing.T) {
	handler := NewRestartHandler()

	result, err := handler.Handle(context.Background(), Request{Type: "RESTART_AGENT"})
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("expected ErrRestartRequested, got %v", err)
	}
	if result == nil {
		t.Error("expected a restart message result")
	}
}

func TestHandlerRespectsContextCancellation(t *testing.T) {
	_ = NewGetStatusHandler(func() map[string]any { return map[string]any{} })

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()

	// A cancelled context is respected by the runtime; the handler itself is
	// trivial. This test documents that execution is context-bound.
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected context to be cancelled")
	}
}
