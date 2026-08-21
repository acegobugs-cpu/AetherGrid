package controller

import (
	"fmt"
	"strings"

	"AetherGrid/operator/api/v1alpha1"
)

// validateSpec enforces the AetherCluster spec invariants. The API server
// enforces static schema validation; this covers the remaining rules the
// controller needs.
func validateSpec(spec v1alpha1.AetherClusterSpec) error {
	if strings.TrimSpace(spec.Image) == "" {
		return fmt.Errorf("spec.image is required")
	}
	if spec.Replicas != nil && *spec.Replicas < 0 {
		return fmt.Errorf("spec.replicas must be >= 0, got %d", *spec.Replicas)
	}
	if spec.Port != 0 && (spec.Port < 1 || spec.Port > 65535) {
		return fmt.Errorf("spec.port must be between 1 and 65535, got %d", spec.Port)
	}
	return nil
}
