package provisioning

import (
	"context"
	"errors"
	"fmt"
)

// ErrorKind classifies a provisioning failure so callers can react without
// parsing provider output.
type ErrorKind string

// Provisioning error kinds.
const (
	KindInvalidSpecification   ErrorKind = "INVALID_SPECIFICATION"
	KindTerraformInitFailed    ErrorKind = "TERRAFORM_INIT_FAILED"
	KindTerraformPlanFailed    ErrorKind = "TERRAFORM_PLAN_FAILED"
	KindTerraformApplyFailed   ErrorKind = "TERRAFORM_APPLY_FAILED"
	KindTerraformDestroyFailed ErrorKind = "TERRAFORM_DESTROY_FAILED"
	KindTerraformStatusFailed  ErrorKind = "TERRAFORM_STATUS_FAILED"
	KindProviderUnavailable    ErrorKind = "PROVIDER_UNAVAILABLE"
	KindOutputUnavailable      ErrorKind = "OUTPUT_UNAVAILABLE"
	KindTimeout                ErrorKind = "TIMEOUT"
	KindCancelled              ErrorKind = "CANCELLED"
)

// Error is a structured provisioning error. The Message must never contain
// secrets, credentials or provider environment variables.
type Error struct {
	Kind    ErrorKind
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Kind, e.Cause)
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// IsKind reports whether err is a provisioning Error with the given kind.
func IsKind(err error, kind ErrorKind) bool {
	var provisioningError *Error
	if !errors.As(err, &provisioningError) {
		return false
	}
	return provisioningError.Kind == kind
}

// IsCancelled reports whether err represents an operation cancelled by context
// cancellation.
func IsCancelled(err error) bool {
	return IsKind(err, KindCancelled) || errors.Is(err, context.Canceled)
}

// IsTimeout reports whether err represents a timeout.
func IsTimeout(err error) bool {
	return IsKind(err, KindTimeout) || errors.Is(err, context.DeadlineExceeded)
}
