package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrorCode is a stable, user-facing classification of a Kubernetes failure.
// Error codes are part of the AETHER-GRID API contract and are never raw
// client-go messages.
type ErrorCode string

// Kubernetes error codes.
const (
	// CodeInvalidConfig reports an unusable Kubernetes client configuration
	// (for example a missing or malformed kubeconfig).
	CodeInvalidConfig ErrorCode = "KUBERNETES_INVALID_CONFIGURATION"
	// CodeUnavailable reports that the Kubernetes API server could not be
	// reached or answered with a service-level failure.
	CodeUnavailable ErrorCode = "KUBERNETES_UNAVAILABLE"
	// CodeUnauthorized reports that the configured credentials were rejected.
	CodeUnauthorized ErrorCode = "KUBERNETES_UNAUTHORIZED"
	// CodeForbidden reports that the configured credentials lack permission
	// for the requested operation.
	CodeForbidden ErrorCode = "KUBERNETES_FORBIDDEN"
	// CodeTimeout reports that a Kubernetes API call exceeded its deadline.
	CodeTimeout ErrorCode = "KUBERNETES_TIMEOUT"
	// CodeNotFound reports that a requested Kubernetes resource does not
	// exist.
	CodeNotFound ErrorCode = "KUBERNETES_RESOURCE_NOT_FOUND"
)

// Error is a translated Kubernetes failure. Raw client-go errors are never
// exposed through AETHER-GRID APIs; they are logged internally for debugging.
type Error struct {
	Code ErrorCode
	Err  error
}

// Error implements error. It exposes only the stable code and a safe message,
// never stack traces or internal details.
func (e *Error) Error() string {
	if e.Err == nil {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, safeMessage(e.Err))
}

// Unwrap exposes the underlying error for errors.Is/As.
func (e *Error) Unwrap() error {
	return e.Err
}

// IsCode reports whether err is a Kubernetes error with the given code.
func IsCode(err error, code ErrorCode) bool {
	var kubernetesError *Error
	if !errors.As(err, &kubernetesError) {
		return false
	}
	return kubernetesError.Code == code
}

// Translate converts a client-go / transport error into a domain error. It is
// the single translation point so the rest of the application never inspects
// raw Kubernetes errors.
func Translate(err error) error {
	if err == nil {
		return nil
	}

	// Already a translated error: pass it through untouched.
	var kubernetesError *Error
	if errors.As(err, &kubernetesError) {
		return err
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Code: CodeTimeout, Err: err}
	case errors.Is(err, context.Canceled):
		return err
	case k8sapierrors.IsUnauthorized(err):
		return &Error{Code: CodeUnauthorized, Err: err}
	case k8sapierrors.IsForbidden(err):
		return &Error{Code: CodeForbidden, Err: err}
	case k8sapierrors.IsNotFound(err):
		return &Error{Code: CodeNotFound, Err: err}
	case k8sapierrors.IsTimeout(err) || k8sapierrors.IsServerTimeout(err):
		return &Error{Code: CodeTimeout, Err: err}
	case k8sapierrors.IsServiceUnavailable(err):
		return &Error{Code: CodeUnavailable, Err: err}
	case isTransportUnavailable(err):
		return &Error{Code: CodeUnavailable, Err: err}
	default:
		return &Error{Code: CodeUnavailable, Err: err}
	}
}

// isTransportUnavailable detects connection-level failures (DNS, refused,
// reset, unreachable) that mean the API server is simply unreachable.
func isTransportUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"connection refused",
		"connection reset",
		"no such host",
		"server misbehaving",
		"connect: network is unreachable",
		"unable to connect",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

// safeMessage strips anything that could carry credentials from a log line.
// client-go does not normally embed tokens, but this is a defensive boundary:
// kubeconfig contents, bearer tokens and certificate material are never logged.
func safeMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, secret := range []string{
		"token=",
		"password=",
		"client-key-data",
		"client-certificate-data",
		"authorization: bearer ",
	} {
		if index := strings.Index(strings.ToLower(message), strings.ToLower(secret)); index >= 0 {
			return "<redacted>"
		}
	}
	return message
}
