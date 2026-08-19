package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
)

func TestValidateSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    func() *v1alpha1.AetherClusterSpec
		wantErr bool
	}{
		{name: "valid default replicas", spec: func() *v1alpha1.AetherClusterSpec {
			return &v1alpha1.AetherClusterSpec{Image: "nginx:stable"}
		}},
		{name: "valid explicit replicas and port", spec: func() *v1alpha1.AetherClusterSpec {
			return &v1alpha1.AetherClusterSpec{Replicas: ptr.To(int32(3)), Image: "nginx:stable", Port: 8080}
		}},
		{name: "empty image rejected", spec: func() *v1alpha1.AetherClusterSpec {
			return &v1alpha1.AetherClusterSpec{Replicas: ptr.To(int32(1))}
		}, wantErr: true},
		{name: "blank image rejected", spec: func() *v1alpha1.AetherClusterSpec {
			return &v1alpha1.AetherClusterSpec{Image: "  "}
		}, wantErr: true},
		{name: "negative replicas rejected", spec: func() *v1alpha1.AetherClusterSpec {
			return &v1alpha1.AetherClusterSpec{Replicas: ptr.To(int32(-1)), Image: "nginx:stable"}
		}, wantErr: true},
		{name: "zero replicas allowed", spec: func() *v1alpha1.AetherClusterSpec {
			return &v1alpha1.AetherClusterSpec{Replicas: ptr.To(int32(0)), Image: "nginx:stable"}
		}},
		{name: "port too large rejected", spec: func() *v1alpha1.AetherClusterSpec {
			return &v1alpha1.AetherClusterSpec{Replicas: ptr.To(int32(1)), Image: "nginx:stable", Port: 70000}
		}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSpec(*tt.spec())
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildDeployment(t *testing.T) {
	cr := testCluster("example", nil)
	deploy, err := buildDeployment(cr, testScheme())
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}

	if deploy.Name != "example" {
		t.Errorf("deployment name = %q, want %q", deploy.Name, "example")
	}
	if deploy.Namespace != cr.Namespace {
		t.Errorf("deployment namespace = %q, want %q", deploy.Namespace, cr.Namespace)
	}
	if got := *deploy.Spec.Replicas; got != 2 {
		t.Errorf("replicas = %d, want 2", got)
	}
	if got := deploy.Spec.Template.Spec.Containers[0].Image; got != "nginx:stable" {
		t.Errorf("image = %q, want nginx:stable", got)
	}
	if got := deploy.Spec.Template.Spec.Containers[0].Name; got != "nginx" {
		t.Errorf("container name = %q, want nginx", got)
	}
	if deploy.Spec.Selector.MatchLabels[LabelName] != "example" {
		t.Errorf("selector label = %v, want example", deploy.Spec.Selector.MatchLabels)
	}
	if deploy.Labels[LabelManaged] != "true" {
		t.Errorf("missing managed label")
	}
	if deploy.Labels[LabelManagedBy] != OperatorName {
		t.Errorf("managed-by = %q, want %q", deploy.Labels[LabelManagedBy], OperatorName)
	}

	ref := metav1.GetControllerOf(deploy)
	if ref == nil {
		t.Fatal("expected a controller owner reference")
	}
	if ref.UID != cr.UID {
		t.Errorf("owner reference UID = %v, want %v", ref.UID, cr.UID)
	}
	if !*ref.Controller {
		t.Error("expected controller owner reference to be set")
	}
}

func TestBuildDeploymentDefaultsReplicasToOne(t *testing.T) {
	cr := testCluster("example", func(cr *v1alpha1.AetherCluster) {
		cr.Spec.Replicas = nil
	})
	deploy, err := buildDeployment(cr, testScheme())
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}
	if got := *deploy.Spec.Replicas; got != 1 {
		t.Errorf("default replicas = %d, want 1", got)
	}
}

func TestBuildDeploymentContainerNameFromImage(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"nginx:stable", "nginx"},
		{"registry.k8s.io/nginx:1.2", "nginx"},
		{"localhost:5000/team/my-app:v1", "my-app"},
		{"ghcr.io/org/app@sha256:abc", "app"},
		{"nginx", "nginx"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			if got := containerName(tt.image); got != tt.want {
				t.Errorf("containerName(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestBuildDeploymentPorts(t *testing.T) {
	cr := testCluster("example", func(cr *v1alpha1.AetherCluster) {
		cr.Spec.Port = 8080
	})
	deploy, err := buildDeployment(cr, testScheme())
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}
	ports := deploy.Spec.Template.Spec.Containers[0].Ports
	if len(ports) != 1 || ports[0].ContainerPort != 8080 {
		t.Fatalf("ports = %+v, want a single 8080 port", ports)
	}

	crNoPort := testCluster("example", nil)
	deployNoPort, err := buildDeployment(crNoPort, testScheme())
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}
	if len(deployNoPort.Spec.Template.Spec.Containers[0].Ports) != 0 {
		t.Error("expected no ports when spec.port is unset")
	}
}

func TestDesiredReplicas(t *testing.T) {
	if got := desiredReplicas(v1alpha1.AetherClusterSpec{}); got != 1 {
		t.Errorf("desiredReplicas(unset) = %d, want 1", got)
	}
	if got := desiredReplicas(v1alpha1.AetherClusterSpec{Replicas: ptr.To(int32(5))}); got != 5 {
		t.Errorf("desiredReplicas(5) = %d, want 5", got)
	}
}
