// Package integration contains the Phase 5 integration test. It runs only when
// INTEGRATION_KUBERNETES=true AND a real cluster is reachable through the
// standard kubeconfig loading rules; otherwise it is skipped so ordinary unit
// tests never require a cluster. kind is the recommended local cluster.
package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
	"github.com/acegobugs-cpu/AetherGrid/operator/internal/controller"
)

const (
	integrationTimeout = 120 * time.Second
	pollInterval       = 2 * time.Second
	integrationNS      = "aether-grid-integration"
	integrationCRDName = "aetherclusters.aether-grid.io"
	integrationCRD     = "aether-grid.io_aetherclusters.yaml"
)

// liveClient returns a real cluster client, skipping when no cluster exists.
func liveClient(t *testing.T) (client.Client, *rest.Config) {
	t.Helper()
	if os.Getenv("INTEGRATION_KUBERNETES") != "true" {
		t.Skip("INTEGRATION_KUBERNETES not set; skipping real-cluster integration test")
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Skipf("no usable Kubernetes configuration (%v); skipping", err)
	}

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add clientgo scheme: %v", err)
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatalf("add apiextensions scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		t.Skipf("cannot build cluster client (%v); skipping", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Get(ctx, types.NamespacedName{Name: "kube-system"}, &corev1.Namespace{}); err != nil {
		t.Skipf("cluster not reachable (%v); skipping", err)
	}
	return c, cfg
}

func waitFor(t *testing.T, ctx context.Context, what string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(integrationTimeout)
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled waiting for %s", what)
		case <-time.After(pollInterval):
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestOperatorIntegration is the Phase 5 end-to-end demonstration against a
// real cluster: CRD install -> reconcile -> ready -> replica drift repaired ->
// image drift repaired -> deletion garbage-collects the Deployment.
func TestOperatorIntegration(t *testing.T) {
	c, _ := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	t.Log("installing the AetherCluster CRD")
	installCRD(t, ctx, c)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: integrationNS}}
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	defer func() {
		_ = c.Delete(context.Background(), ns)
	}()

	r := &controller.AetherClusterReconciler{Client: c, Scheme: c.Scheme()}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: integrationNS, Name: "example"}}

	cr := &v1alpha1.AetherCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: integrationNS},
		Spec: v1alpha1.AetherClusterSpec{
			Replicas: ptr.To(int32(2)),
			Image:    "nginx:stable",
			Port:     80,
		},
	}
	if err := c.Create(ctx, cr); err != nil {
		t.Fatalf("create AetherCluster: %v", err)
	}
	defer func() {
		_ = c.Delete(context.Background(), cr)
	}()

	// Step 1: the operator creates the Deployment.
	waitFor(t, ctx, "Deployment creation", func() error {
		_, err := r.Reconcile(ctx, req)
		return err
	})
	var deploy appsv1.Deployment
	if err := c.Get(ctx, client.ObjectKey{Namespace: integrationNS, Name: "example"}, &deploy); err != nil {
		t.Fatalf("managed Deployment was not created: %v", err)
	}
	if *deploy.Spec.Replicas != 2 {
		t.Fatalf("Deployment replicas = %d, want 2", *deploy.Spec.Replicas)
	}

	// Step 2: pods become ready and the CR reports Ready.
	waitFor(t, ctx, "Deployment readiness", func() error {
		if _, err := r.Reconcile(ctx, req); err != nil {
			return err
		}
		if err := c.Get(ctx, client.ObjectKey{Namespace: integrationNS, Name: "example"}, &deploy); err != nil {
			return err
		}
		if deploy.Status.ReadyReplicas < *deploy.Spec.Replicas {
			return apierrors.NewServiceUnavailable("deployment not ready yet")
		}
		return nil
	})
	waitFor(t, ctx, "AetherCluster Ready phase", func() error {
		if _, err := r.Reconcile(ctx, req); err != nil {
			return err
		}
		var cur v1alpha1.AetherCluster
		if err := c.Get(ctx, req.NamespacedName, &cur); err != nil {
			return err
		}
		if cur.Status.Phase != v1alpha1.PhaseReady {
			return apierrors.NewServiceUnavailable("phase not ready yet: " + string(cur.Status.Phase))
		}
		return nil
	})

	// Step 3: replica drift (scale out-of-band to 1) is corrected back to 2.
	if err := c.Get(ctx, client.ObjectKey{Namespace: integrationNS, Name: "example"}, &deploy); err != nil {
		t.Fatalf("fetch Deployment: %v", err)
	}
	deploy.Spec.Replicas = ptr.To(int32(1))
	if err := c.Update(ctx, &deploy); err != nil {
		t.Fatalf("introduce replica drift: %v", err)
	}
	waitFor(t, ctx, "replica drift correction", func() error {
		if _, err := r.Reconcile(ctx, req); err != nil {
			return err
		}
		if err := c.Get(ctx, client.ObjectKey{Namespace: integrationNS, Name: "example"}, &deploy); err != nil {
			return err
		}
		if *deploy.Spec.Replicas != 2 {
			return apierrors.NewServiceUnavailable("replicas not corrected")
		}
		return nil
	})

	// Step 4: image drift is corrected back to nginx:stable.
	if err := c.Get(ctx, client.ObjectKey{Namespace: integrationNS, Name: "example"}, &deploy); err != nil {
		t.Fatalf("fetch Deployment: %v", err)
	}
	deploy.Spec.Template.Spec.Containers[0].Image = "nginx:latest"
	if err := c.Update(ctx, &deploy); err != nil {
		t.Fatalf("introduce image drift: %v", err)
	}
	waitFor(t, ctx, "image drift correction", func() error {
		if _, err := r.Reconcile(ctx, req); err != nil {
			return err
		}
		if err := c.Get(ctx, client.ObjectKey{Namespace: integrationNS, Name: "example"}, &deploy); err != nil {
			return err
		}
		if deploy.Spec.Template.Spec.Containers[0].Image != "nginx:stable" {
			return apierrors.NewServiceUnavailable("image not corrected")
		}
		return nil
	})

	// Step 5: deleting the AetherCluster garbage-collects the owned Deployment.
	if err := c.Delete(ctx, cr); err != nil {
		t.Fatalf("delete AetherCluster: %v", err)
	}
	waitFor(t, ctx, "Deployment garbage collection", func() error {
		return c.Get(ctx, client.ObjectKey{Namespace: integrationNS, Name: "example"}, &appsv1.Deployment{})
	})
}

func installCRD(t *testing.T, ctx context.Context, c client.Client) {
	t.Helper()
	var existing apiextensionsv1.CustomResourceDefinition
	err := c.Get(ctx, types.NamespacedName{Name: integrationCRDName}, &existing)
	if err == nil {
		return
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("check CRD: %v", err)
	}

	path := filepath.Join("..", "config", "crd", "bases", integrationCRD)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CRD manifest: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parse CRD manifest: %v", err)
	}
	if err := c.Create(ctx, &crd); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create CRD: %v", err)
	}

	waitFor(t, ctx, "CRD established", func() error {
		var cur apiextensionsv1.CustomResourceDefinition
		if err := c.Get(ctx, types.NamespacedName{Name: integrationCRDName}, &cur); err != nil {
			return err
		}
		for _, cond := range cur.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				return nil
			}
		}
		return apierrors.NewServiceUnavailable("CRD not established yet")
	})
}
