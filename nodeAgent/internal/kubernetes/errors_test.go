package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestTranslatePassesThroughDomainError(t *testing.T) {
	original := &Error{Code: CodeForbidden}
	translated := Translate(original)
	if !errors.Is(translated, original) {
		t.Fatalf("expected the same error back, got %T", translated)
	}
	if !IsCode(translated, CodeForbidden) {
		t.Fatal("expected CodeForbidden")
	}
}

func TestTranslateAuthorization(t *testing.T) {
	tests := []struct {
		name   string
		input  error
		expect ErrorCode
	}{
		{"unauthorized", apierrors.NewUnauthorized("nope"), CodeUnauthorized},
		{"forbidden", apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "name", errors.New("nope")), CodeForbidden},
		{"not found", apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "x"), CodeNotFound},
		{"timeout status", apierrors.NewTimeoutError("slow", 1), CodeTimeout},
		{"service unavailable", apierrors.NewServiceUnavailable("down"), CodeUnavailable},
		{"server timeout", apierrors.NewServerTimeout(schema.GroupResource{Resource: "pods"}, "list", 1), CodeTimeout},
		{"context deadline", context.DeadlineExceeded, CodeTimeout},
		{"connection refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, CodeUnavailable},
		{"generic", errors.New("boom"), CodeUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translated := Translate(test.input)
			if !IsCode(translated, test.expect) {
				t.Errorf("expected code %s, got %v", test.expect, translated)
			}
		})
	}
}

func TestTranslateCanceledPassthrough(t *testing.T) {
	if got := Translate(context.Canceled); got != context.Canceled {
		t.Fatalf("expected context.Canceled passthrough, got %v", got)
	}
}

func TestIsCodeFalseForOtherErrors(t *testing.T) {
	if IsCode(errors.New("plain"), CodeUnavailable) {
		t.Fatal("expected false for non-Kubernetes error")
	}
	if IsCode(nil, CodeUnavailable) {
		t.Fatal("expected false for nil")
	}
}

func TestErrorNeverLeaksCredentials(t *testing.T) {
	err := &Error{Code: CodeUnavailable, Err: fmt.Errorf("dial tcp failed, token=supersecret for server")}
	if message := err.Error(); containsAny(message, "supersecret", "token=") {
		t.Fatalf("error message leaked a credential: %q", message)
	}
}

func TestNotFoundComposition(t *testing.T) {
	err := Translate(apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "x"))
	if !IsCode(err, CodeNotFound) {
		t.Fatalf("expected CodeNotFound, got %v", err)
	}
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if len(fragment) > 0 && contains(value, fragment) {
			return true
		}
	}
	return false
}

func contains(value, fragment string) bool {
	for i := 0; i+len(fragment) <= len(value); i++ {
		if value[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
