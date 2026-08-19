package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
)

// observeDeployment reports readiness and degradation from the Deployment
// status alone. It never reports Ready unless the observed Deployment state
// satisfies the desired conditions.
func observeDeployment(deploy *appsv1.Deployment) (ready, degraded bool, message string) {
	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	if deploy.Status.ReadyReplicas >= desired && deploy.Status.AvailableReplicas >= desired {
		ready = true
	}

	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionFalse {
			degraded = true
			message = cond.Message
		}
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse {
			degraded = true
			if message == "" {
				message = cond.Message
			}
		}
	}

	return ready, degraded, message
}

// setCondition upserts a condition on the AetherCluster status following
// Kubernetes conventions.
func setCondition(cr *v1alpha1.AetherCluster, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cr.Generation,
	})
}

// reconcileStatus recomputes the AetherCluster phase, conditions and replica
// counts from the current Deployment observation. invalid marks an
// unrecoverable spec validation failure. deploy may be nil while the
// Deployment is still being created.
func reconcileStatus(cr *v1alpha1.AetherCluster, deploy *appsv1.Deployment, invalid bool, deployErr string) {
	cr.Status.ObservedGeneration = cr.Generation

	if invalid {
		cr.Status.Phase = v1alpha1.PhaseFailed
		setCondition(cr, v1alpha1.ConditionReady, metav1.ConditionFalse, v1alpha1.ReasonValidationFailed, deployErr)
		setCondition(cr, v1alpha1.ConditionProgressing, metav1.ConditionFalse, v1alpha1.ReasonValidationFailed, deployErr)
		return
	}

	desired := desiredReplicas(cr.Spec)

	if deploy == nil {
		cr.Status.Phase = v1alpha1.PhaseProgressing
		cr.Status.DesiredReplicas = desired
		setCondition(cr, v1alpha1.ConditionReady, metav1.ConditionFalse, v1alpha1.ReasonCreatingDeployment, "Deployment does not exist yet")
		setCondition(cr, v1alpha1.ConditionProgressing, metav1.ConditionTrue, v1alpha1.ReasonCreatingDeployment, "Creating managed Deployment")
		return
	}

	cr.Status.ReadyReplicas = deploy.Status.ReadyReplicas
	cr.Status.DesiredReplicas = desired

	ready, degraded, message := observeDeployment(deploy)

	switch {
	case degraded:
		cr.Status.Phase = v1alpha1.PhaseDegraded
		setCondition(cr, v1alpha1.ConditionReady, metav1.ConditionFalse, v1alpha1.ReasonDeploymentDegraded, message)
		setCondition(cr, v1alpha1.ConditionProgressing, metav1.ConditionFalse, v1alpha1.ReasonDeploymentDegraded, message)
		setCondition(cr, v1alpha1.ConditionDegraded, metav1.ConditionTrue, v1alpha1.ReasonDeploymentDegraded, message)
	case ready:
		cr.Status.Phase = v1alpha1.PhaseReady
		setCondition(cr, v1alpha1.ConditionReady, metav1.ConditionTrue, v1alpha1.ReasonDeploymentReady, "Deployment is running the desired replicas")
		setCondition(cr, v1alpha1.ConditionProgressing, metav1.ConditionFalse, v1alpha1.ReasonDeploymentReady, "Deployment is ready")
		setCondition(cr, v1alpha1.ConditionDegraded, metav1.ConditionFalse, v1alpha1.ReasonDeploymentReady, "Deployment is not degraded")
	default:
		cr.Status.Phase = v1alpha1.PhaseProgressing
		setCondition(cr, v1alpha1.ConditionReady, metav1.ConditionFalse, v1alpha1.ReasonDeploymentProgressing, "Deployment is converging toward the desired state")
		setCondition(cr, v1alpha1.ConditionProgressing, metav1.ConditionTrue, v1alpha1.ReasonDeploymentProgressing, "Deployment is progressing")
	}
}
