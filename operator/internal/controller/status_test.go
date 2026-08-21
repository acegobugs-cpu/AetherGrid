package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"AetherGrid/operator/api/v1alpha1"
)

func conditionOf(t *testing.T, cr *v1alpha1.AetherCluster, condType string) metav1.Condition {
	t.Helper()
	cond := meta.FindStatusCondition(cr.Status.Conditions, condType)
	if cond == nil {
		t.Fatalf("condition %q not present in %+v", condType, cr.Status.Conditions)
	}
	return *cond
}

func TestObserveDeploymentReady(t *testing.T) {
	deploy := deploymentForTest("nginx:stable", 2)
	deploy.Spec.Replicas = ptr.To(int32(2))
	deploy.Status.ReadyReplicas = 2
	deploy.Status.AvailableReplicas = 2
	deploy.Status.UpdatedReplicas = 2

	ready, degraded, _ := observeDeployment(deploy)
	if !ready {
		t.Error("expected ready=true")
	}
	if degraded {
		t.Error("expected degraded=false")
	}
}

func TestObserveDeploymentNotReady(t *testing.T) {
	deploy := deploymentForTest("nginx:stable", 2)
	deploy.Status.ReadyReplicas = 1
	deploy.Status.AvailableReplicas = 1

	ready, degraded, _ := observeDeployment(deploy)
	if ready {
		t.Error("expected ready=false")
	}
	if degraded {
		t.Error("expected degraded=false")
	}
}

func TestObserveDeploymentDegraded(t *testing.T) {
	deploy := deploymentForTest("nginx:stable", 2)
	deploy.Status.Conditions = []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, Message: "MinimumReplicasUnavailable"},
	}

	ready, degraded, msg := observeDeployment(deploy)
	if ready {
		t.Error("expected ready=false")
	}
	if !degraded {
		t.Error("expected degraded=true")
	}
	if msg == "" {
		t.Error("expected a degradation message")
	}
}

func TestReconcileStatusInvalidSpec(t *testing.T) {
	cr := testCluster("example", nil)
	reconcileStatus(cr, nil, true, "spec.image is required")

	if cr.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want %q", cr.Status.Phase, v1alpha1.PhaseFailed)
	}
	if got := conditionOf(t, cr, v1alpha1.ConditionReady); got.Status != metav1.ConditionFalse {
		t.Errorf("ready condition = %v, want False", got.Status)
	}
}

func TestReconcileStatusCreating(t *testing.T) {
	cr := testCluster("example", nil)
	reconcileStatus(cr, nil, false, "")

	if cr.Status.Phase != v1alpha1.PhaseProgressing {
		t.Errorf("phase = %q, want %q", cr.Status.Phase, v1alpha1.PhaseProgressing)
	}
	if got := conditionOf(t, cr, v1alpha1.ConditionReady); got.Status != metav1.ConditionFalse {
		t.Errorf("ready condition = %v, want False", got.Status)
	}
	if got := conditionOf(t, cr, v1alpha1.ConditionProgressing); got.Status != metav1.ConditionTrue {
		t.Errorf("progressing condition = %v, want True", got.Status)
	}
	if cr.Status.ObservedGeneration != 1 {
		t.Errorf("observedGeneration = %d, want 1", cr.Status.ObservedGeneration)
	}
}

func TestReconcileStatusReady(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := deploymentForTest("nginx:stable", 2)
	deploy.Status.ReadyReplicas = 2
	deploy.Status.AvailableReplicas = 2

	reconcileStatus(cr, deploy, false, "")

	if cr.Status.Phase != v1alpha1.PhaseReady {
		t.Errorf("phase = %q, want %q", cr.Status.Phase, v1alpha1.PhaseReady)
	}
	if got := conditionOf(t, cr, v1alpha1.ConditionReady); got.Status != metav1.ConditionTrue {
		t.Errorf("ready condition = %v, want True", got.Status)
	}
	if cr.Status.ReadyReplicas != 2 || cr.Status.DesiredReplicas != 2 {
		t.Errorf("replicas = %d/%d, want 2/2", cr.Status.ReadyReplicas, cr.Status.DesiredReplicas)
	}
}

func TestReconcileStatusDegraded(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := deploymentForTest("nginx:stable", 2)
	deploy.Status.Conditions = []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, Message: "MinimumReplicasUnavailable"},
	}

	reconcileStatus(cr, deploy, false, "")

	if cr.Status.Phase != v1alpha1.PhaseDegraded {
		t.Errorf("phase = %q, want %q", cr.Status.Phase, v1alpha1.PhaseDegraded)
	}
	if got := conditionOf(t, cr, v1alpha1.ConditionDegraded); got.Status != metav1.ConditionTrue {
		t.Errorf("degraded condition = %v, want True", got.Status)
	}
}

func TestReconcileStatusNeverReportsReadyWithoutReadiness(t *testing.T) {
	cr := testCluster("example", nil)
	deploy := deploymentForTest("nginx:stable", 2)
	deploy.Status.ReadyReplicas = 0
	deploy.Status.AvailableReplicas = 0

	reconcileStatus(cr, deploy, false, "")

	if cr.Status.Phase == v1alpha1.PhaseReady {
		t.Error("phase must not be Ready while the Deployment is not ready")
	}
}
