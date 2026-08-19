// Package v1alpha1 contains the AetherCluster custom resource API definition.
// The v1alpha1 version signals an experimental API that may evolve.
//
// +kubebuilder:object:generate=true
// +groupName=aether-grid.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// GroupName is the AETHER-GRID API group.
const GroupName = "aether-grid.io"

// GroupVersion is the v1alpha1 group version.
var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

// SchemeBuilder collects the scheme registration for this API version.
var SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

// AddToScheme adds this API version to the given scheme.
func AddToScheme(s *runtime.Scheme) error {
	return SchemeBuilder.AddToScheme(s)
}
