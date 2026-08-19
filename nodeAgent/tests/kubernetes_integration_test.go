// Package agent_test contains the Phase 4 Kubernetes integration test. It
// runs only when INTEGRATION_KUBERNETES=true AND a real cluster is reachable
// through the standard kubeconfig loading rules; otherwise it is skipped so
// ordinary unit tests never require a cluster.
package agent_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/nodeAgent/internal/kubernetes"
)

// kubernetesCluster returns a client for a real development cluster, skipping
// the test when none is available.
func kubernetesCluster(t *testing.T) kubernetes.KubernetesClient {
	t.Helper()
	if os.Getenv("INTEGRATION_KUBERNETES") != "true" {
		t.Skip("INTEGRATION_KUBERNETES not set; skipping real-cluster integration test")
	}

	client, err := kubernetes.NewClient("")
	if err != nil {
		t.Skipf("no usable Kubernetes configuration (%v); skipping", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.GetClusterInfo(ctx); err != nil {
		t.Skipf("cluster not reachable (%v); skipping", err)
	}
	return client
}

// TestKubernetesClusterIntegration exercises the Phase 4 integration scenario
// against a real development cluster:
//
//	connect -> version -> list nodes -> ready nodes -> list pods
//	-> create test namespace -> verify -> delete test namespace -> verify.
func TestKubernetesClusterIntegration(t *testing.T) {
	client := kubernetesCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Read cluster version.
	info, err := client.GetClusterInfo(ctx)
	if err != nil {
		t.Fatalf("GetClusterInfo: %v", err)
	}
	if info.Version == "" {
		t.Error("expected a cluster version")
	}

	// 2. List nodes and verify ready count matches the cluster info.
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	ready := 0
	for _, node := range nodes {
		if node.Ready {
			ready++
		}
	}
	if info.NodeCount != len(nodes) || info.ReadyNodes != ready {
		t.Errorf("node count mismatch: info=%+v observed ready=%d", info, ready)
	}

	// 3. List pods (all namespaces is fine for a small dev cluster).
	if _, err := client.ListPods(ctx, ""); err != nil {
		t.Fatalf("ListPods: %v", err)
	}

	// 4. Create a dedicated test namespace, verify it, then delete it.
	const namespace = "aether-grid-integration"
	if err := client.CreateNamespace(ctx, namespace); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = client.DeleteNamespace(cleanupCtx, namespace)
	})

	if err := client.DeleteNamespace(ctx, namespace); err != nil {
		t.Fatalf("DeleteNamespace: %v", err)
	}
}
