package controller

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
)

const testNS = "aether-grid-test"

func reconcileRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testNS, Name: "example"}}
}

// countingClient counts non-status Update calls, used to assert idempotency.
type countingClient struct {
	client.Client
	updates int
}

func (c *countingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.updates++
	return c.Client.Update(ctx, obj, opts...)
}

func newReconciler(t *testing.T, seed ...client.Object) (*AetherClusterReconciler, *countingClient) {
	t.Helper()
	builder := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithStatusSubresource(&v1alpha1.AetherCluster{}).
		WithStatusSubresource(&appsv1.Deployment{}).
		WithObjects(seed...)
	inner := builder.Build()
	counting := &countingClient{Client: inner}
	return &AetherClusterReconciler{Client: counting, Scheme: testScheme()}, counting
}

// ownedDeployment builds a Deployment already controlled by the CR.
func ownedDeployment(cr *v1alpha1.AetherCluster) *appsv1.Deployment {
	deploy, err := buildDeployment(cr, testScheme())
	if err != nil {
		panic(err)
	}
	deploy.ResourceVersion = "1"
	return deploy
}

func readyDeployment(cr *v1alpha1.AetherCluster) *appsv1.Deployment {
	deploy := ownedDeployment(cr)
	deploy.Status.ReadyReplicas = *deploy.Spec.Replicas
	deploy.Status.AvailableReplicas = *deploy.Spec.Replicas
	deploy.Status.UpdatedReplicas = *deploy.Spec.Replicas
	return deploy
}

// TestReconcileAetherClusterCreated tests that a new AetherCluster results in a
// managed Deployment and a Progressing status.
func TestReconcileAetherClusterCreated(t *testing.T) {
	cr := testCluster("example", nil)
	r, _ := newReconciler(t, cr)

	res, err := r.Reconcile(context.Background(), reconcileRequest())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "example"}, &deploy); err != nil {
		t.Fatalf("expected managed Deployment to be created: %v", err)
	}
	if *deploy.Spec.Replicas != 2 || deploy.Spec.Template.Spec.Containers[0].Image != "nginx:stable" {
		t.Errorf("deployment does not match desired state: %+v", deploy.Spec)
	}
	if ref := metav1.GetControllerOf(&deploy); ref == nil || ref.UID != cr.UID {
		t.Error("deployment is not owned by the AetherCluster")
	}

	var updated v1alpha1.AetherCluster
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cr), &updated); err != nil {
		t.Fatalf("fetch CR: %v", err)
	}
	if updated.Status.Phase != v1alpha1.PhaseProgressing {
		t.Errorf("phase = %q, want Progressing", updated.Status.Phase)
	}
	if res.RequeueAfter != RequeueInterval {
		t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, RequeueInterval)
	}
}

// TestReconcileDeploymentAlreadyInSync verifies the operator does not write
// anything when the Deployment already matches the desired state.
func TestReconcileDeploymentAlreadyInSync(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := readyDeployment(cr)
	r, counting := newReconciler(t, cr, deploy)
	cr.ResourceVersion = "1"
	deploy.ResourceVersion = "1"

	// First reconcile writes status (Progressing -> Ready transition) and must
	// not touch the Deployment.
	res, err := r.Reconcile(context.Background(), reconcileRequest())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if counting.updates != 0 {
		t.Errorf("expected 0 Deployment updates for an in-sync Deployment, got %d", counting.updates)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("ready phase should not requeue, got %v", res.RequeueAfter)
	}

	var updated v1alpha1.AetherCluster
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cr), &updated); err != nil {
		t.Fatalf("fetch CR: %v", err)
	}
	if updated.Status.Phase != v1alpha1.PhaseReady {
		t.Errorf("phase = %q, want Ready", updated.Status.Phase)
	}
}

// TestReconcileReplicaDriftRepaired scales the Deployment down out-of-band and
// verifies the operator restores the desired count.
func TestReconcileReplicaDriftRepaired(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := ownedDeployment(cr)
	deploy.Status.ReadyReplicas = 2
	deploy.Status.AvailableReplicas = 2
	deploy.Spec.Replicas = ptr.To(int32(1)) // out-of-band scale to 1
	deploy.ResourceVersion = "1"

	r, counting := newReconciler(t, cr, deploy)
	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var updated appsv1.Deployment
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "example"}, &updated); err != nil {
		t.Fatalf("fetch Deployment: %v", err)
	}
	if got := *updated.Spec.Replicas; got != 2 {
		t.Errorf("replicas after reconcile = %d, want 2 (drift corrected)", got)
	}
	if counting.updates != 1 {
		t.Errorf("expected exactly 1 Deployment update, got %d", counting.updates)
	}
}

// TestReconcileImageDriftRepaired changes the Deployment image out-of-band and
// verifies the operator restores the desired image without recreating.
func TestReconcileImageDriftRepaired(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := ownedDeployment(cr)
	deploy.Status.ReadyReplicas = 2
	deploy.Status.AvailableReplicas = 2
	deploy.Spec.Template.Spec.Containers[0].Image = "nginx:latest" // drift
	deploy.ResourceVersion = "1"

	r, counting := newReconciler(t, cr, deploy)
	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var updated appsv1.Deployment
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "example"}, &updated); err != nil {
		t.Fatalf("fetch Deployment: %v", err)
	}
	if got := updated.Spec.Template.Spec.Containers[0].Image; got != "nginx:stable" {
		t.Errorf("image after reconcile = %q, want nginx:stable (drift corrected)", got)
	}
	if counting.updates != 1 {
		t.Errorf("expected exactly 1 Deployment update, got %d", counting.updates)
	}
}

// TestReconcileDeploymentBecomesReady verifies the CR reports Ready once the
// Deployment status reports the desired replicas.
func TestReconcileDeploymentBecomesReady(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := ownedDeployment(cr)
	deploy.ResourceVersion = "1"

	r, _ := newReconciler(t, cr, deploy)
	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var updated v1alpha1.AetherCluster
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cr), &updated); err != nil {
		t.Fatalf("fetch CR: %v", err)
	}
	if updated.Status.Phase != v1alpha1.PhaseProgressing {
		t.Errorf("phase = %q, want Progressing", updated.Status.Phase)
	}

	// Deployment becomes ready.
	deploy.Status.ReadyReplicas = 2
	deploy.Status.AvailableReplicas = 2
	deploy.Status.UpdatedReplicas = 2
	if err := r.Status().Update(context.Background(), deploy); err != nil {
		t.Fatalf("seed deployment readiness: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cr), &updated); err != nil {
		t.Fatalf("fetch CR: %v", err)
	}
	if updated.Status.Phase != v1alpha1.PhaseReady {
		t.Errorf("phase = %q, want Ready", updated.Status.Phase)
	}
	if updated.Status.ReadyReplicas != 2 {
		t.Errorf("readyReplicas = %d, want 2", updated.Status.ReadyReplicas)
	}
}

// TestReconcileDeploymentDegraded verifies the CR reports Degraded when the
// Deployment reports an unavailable condition.
func TestReconcileDeploymentDegraded(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := ownedDeployment(cr)
	deploy.Status.Conditions = []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: "False", Message: "MinimumReplicasUnavailable"},
	}
	deploy.ResourceVersion = "1"

	r, _ := newReconciler(t, cr, deploy)
	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var updated v1alpha1.AetherCluster
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cr), &updated); err != nil {
		t.Fatalf("fetch CR: %v", err)
	}
	if updated.Status.Phase != v1alpha1.PhaseDegraded {
		t.Errorf("phase = %q, want Degraded", updated.Status.Phase)
	}
}

// TestReconcileInvalidSpec verifies an invalid spec is reported as Failed and
// no Deployment is created.
func TestReconcileInvalidSpec(t *testing.T) {
	cr := testCluster("example", func(cr *v1alpha1.AetherCluster) {
		cr.Spec.Image = ""
	})
	r, _ := newReconciler(t, cr)

	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile should not return an error for a spec validation failure: %v", err)
	}

	var deploy appsv1.Deployment
	err := r.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "example"}, &deploy)
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected no Deployment for invalid spec, got err=%v", err)
	}

	var updated v1alpha1.AetherCluster
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cr), &updated); err != nil {
		t.Fatalf("fetch CR: %v", err)
	}
	if updated.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want Failed", updated.Status.Phase)
	}
}

// TestReconcileDeletedCR verifies a deleted AetherCluster is a no-op: garbage
// collection of the owned Deployment is Kubernetes' responsibility.
func TestReconcileDeletedCR(t *testing.T) {
	cr := testCluster("example", nil)
	r, _ := newReconciler(t, cr)

	if err := r.Delete(context.Background(), cr); err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	res, err := r.Reconcile(context.Background(), reconcileRequest())
	if err != nil {
		t.Fatalf("Reconcile after delete must not error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("deleted CR should not requeue, got %v", res.RequeueAfter)
	}
}

// TestReconcileOperatorRestart verifies reconciliation is stateless: after a
// simulated restart the operator rediscovers the existing Deployment and
// converges without creating a duplicate or erroring.
func TestReconcileOperatorRestart(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := ownedDeployment(cr)
	deploy.Status.ReadyReplicas = 2
	deploy.Status.AvailableReplicas = 2

	r, _ := newReconciler(t, cr, deploy)
	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var list appsv1.DeploymentList
	if err := r.List(context.Background(), &list, client.InNamespace(testNS)); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected exactly one Deployment after restart reconcile, got %d", len(list.Items))
	}
}

// TestReconcileAPITemporarilyUnavailable verifies the operator surfaces API
// errors so controller-runtime can retry with backoff rather than crashing.
func TestReconcileAPITemporarilyUnavailable(t *testing.T) {
	cr := testCluster("example", nil)
	r, _ := newReconciler(t, cr)

	boom := errors.New("connection refused")
	r.Client = &erroringClient{Client: r.Client, err: boom}

	_, err := r.Reconcile(context.Background(), reconcileRequest())
	if err == nil {
		t.Fatal("expected an error to be returned so controller-runtime can retry")
	}
}

// TestReconcileSecondRunIsIdempotent verifies a second reconcile of an in-sync
// Deployment performs no writes at all (no Deployment update, no status write).
func TestReconcileSecondRunIsIdempotent(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := readyDeployment(cr)
	r, counting := newReconciler(t, cr, deploy)
	cr.ResourceVersion = "1"
	deploy.ResourceVersion = "1"

	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	deployCalls := counting.updates
	if deployCalls != 0 {
		t.Fatalf("first reconcile should not update Deployment, got %d", deployCalls)
	}

	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if counting.updates != deployCalls {
		t.Errorf("second reconcile wrote to the Deployment (%d updates)", counting.updates)
	}
}

// erroringClient fails every operation with the given error.
type erroringClient struct {
	client.Client
	err error
}

func (c *erroringClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.err
}
