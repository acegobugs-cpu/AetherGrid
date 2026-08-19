package controller

import (
	"fmt"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// buildDeployment constructs the desired Deployment for the given
// AetherCluster. It is pure: it performs no I/O and can be unit-tested without
// a cluster. The Deployment name and namespace mirror the AetherCluster so the
// mapping between the CR and its managed resource is always explicit.
func buildDeployment(cr *v1alpha1.AetherCluster, scheme *runtime.Scheme) (*appsv1.Deployment, error) {
	replicas := desiredReplicas(cr.Spec)

	labels := managedLabels(cr)
	podLabels := map[string]string{
		LabelName: cr.Name,
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: podLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  containerName(cr.Spec.Image),
							Image: cr.Spec.Image,
							Ports: containerPorts(cr.Spec),
						},
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(cr, deploy, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return deploy, nil
}

// managedLabels returns the subset of labels the operator owns and enforces on
// the managed Deployment.
func managedLabels(cr *v1alpha1.AetherCluster) map[string]string {
	return map[string]string{
		LabelManaged:   "true",
		LabelManagedBy: OperatorName,
		LabelName:      cr.Name,
		LabelPartOf:    "aether-grid",
	}
}

// desiredReplicas returns the desired replica count, defaulting to 1 when the
// spec omits it.
func desiredReplicas(spec v1alpha1.AetherClusterSpec) int32 {
	if spec.Replicas == nil {
		return 1
	}
	return *spec.Replicas
}

// containerName derives a stable, DNS-compliant container name from the image
// reference (for example "nginx:stable" becomes "nginx"). The container name
// is an owned field: drift is detected and corrected like image drift.
func containerName(image string) string {
	name := image
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[:i]
	}
	name = strings.ToLower(name)
	name = nonAlnum.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "app"
	}
	if len(name) > 63 {
		name = strings.Trim(name[:63], "-")
	}
	return name
}

// containerPorts maps the optional spec.port onto a container port list.
func containerPorts(spec v1alpha1.AetherClusterSpec) []corev1.ContainerPort {
	if spec.Port == 0 {
		return nil
	}
	return []corev1.ContainerPort{
		{
			Name:          "app",
			ContainerPort: spec.Port,
		},
	}
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
