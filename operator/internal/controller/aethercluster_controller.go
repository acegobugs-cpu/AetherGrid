package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
)

// AetherClusterReconciler reconciles AetherCluster resources. It is stateless
// with respect to reconciliation progress: the Kubernetes API is the source of
// truth, so restarts are harmless.
type AetherClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=aether-grid.io,resources=aetherclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aether-grid.io,resources=aetherclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aether-grid.io,resources=aetherclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile drives an AetherCluster toward its desired state. The loop is
// idempotent: every step reads current state from the API and only writes when
// something actually differs.
func (r *AetherClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr v1alpha1.AetherCluster
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			// The resource was deleted; owner references trigger garbage
			// collection of the managed Deployment, so nothing to do.
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to fetch AetherCluster")
		return ctrl.Result{}, err
	}

	if err := validateSpec(cr.Spec); err != nil {
		logger.Info("AetherCluster spec invalid", "name", cr.Name, "error", err.Error())
		reconcileStatus(&cr, nil, true, err.Error())
		return ctrl.Result{}, r.updateStatus(ctx, &cr)
	}

	desired, err := buildDeployment(&cr, r.Scheme)
	if err != nil {
		logger.Error(err, "failed to build desired Deployment")
		return ctrl.Result{}, err
	}

	var actual appsv1.Deployment
	getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), &actual)
	deployExists := getErr == nil
	changed := false

	switch {
	case apierrors.IsNotFound(getErr):
		// Create the managed Deployment and let Kubernetes watch events drive
		// the next reconcile.
		if err := r.Create(ctx, desired); err != nil {
			logger.Error(err, "failed to create Deployment")
			return ctrl.Result{}, err
		}
		changed = true
		logger.Info("created Deployment", "deployment", desired.Name)

	case getErr != nil:
		logger.Error(getErr, "failed to fetch Deployment")
		return ctrl.Result{}, getErr

	default:
		if !ownedBy(&actual, &cr) {
			err := fmt.Errorf("Deployment %s/%s already exists and is not controlled by this AetherCluster",
				actual.Namespace, actual.Name)
			logger.Error(err, "ownership conflict")
			return ctrl.Result{}, err
		}

		diff := compareDeployment(desired, &actual)
		if diff.needsRecreate {
			logger.Info("Deployment selector drift detected; recreating", "deployment", actual.Name)
			if err := r.Delete(ctx, &actual); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "failed to delete drifted Deployment")
				return ctrl.Result{}, err
			}
			reconcileStatus(&cr, nil, false, "")
			if err := r.updateStatus(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: RequeueInterval}, nil
		}
		if diff.needsUpdate {
			applyDesired(&actual, desired)
			if err := r.Update(ctx, &actual); err != nil {
				logger.Error(err, "failed to update Deployment")
				return ctrl.Result{}, err
			}
			changed = true
			logger.Info("updated Deployment to correct drift",
				"deployment", actual.Name, "drift", diff.reasons)
		}
	}

	// Observe the Deployment. After a create/update the API copy is stale, so
	// refresh it to read authoritative status.
	var observed appsv1.Deployment
	if deployExists && !changed {
		observed = actual
	} else {
		if err := r.Get(ctx, client.ObjectKeyFromObject(desired), &observed); err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to re-fetch Deployment")
			return ctrl.Result{}, err
		}
	}

	reconcileStatus(&cr, orNil(&observed), false, "")
	if err := r.updateStatus(ctx, &cr); err != nil {
		logger.Error(err, "failed to update AetherCluster status")
		return ctrl.Result{}, err
	}

	if cr.Status.Phase != v1alpha1.PhaseReady {
		return ctrl.Result{RequeueAfter: RequeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

// updateStatus writes the CR status only when it actually changed, keeping
// reconciliation idempotent and avoiding a self-triggering reconcile loop.
func (r *AetherClusterReconciler) updateStatus(ctx context.Context, cr *v1alpha1.AetherCluster) error {
	var current v1alpha1.AetherCluster
	if err := r.Get(ctx, client.ObjectKeyFromObject(cr), &current); err != nil {
		return err
	}
	if statusEqual(&current.Status, &cr.Status) {
		return nil
	}
	return r.Status().Update(ctx, cr)
}

// statusEqual compares statuses ignoring LastTransitionTime, which Kubernetes
// sets on its own during transitions.
func statusEqual(a, b *v1alpha1.AetherClusterStatus) bool {
	if a.Phase != b.Phase || a.ReadyReplicas != b.ReadyReplicas ||
		a.DesiredReplicas != b.DesiredReplicas || a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	if len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for _, ac := range a.Conditions {
		var match *metav1.Condition
		for i := range b.Conditions {
			if b.Conditions[i].Type == ac.Type {
				match = &b.Conditions[i]
				break
			}
		}
		if match == nil || match.Status != ac.Status || match.Reason != ac.Reason ||
			match.Message != ac.Message || match.ObservedGeneration != ac.ObservedGeneration {
			return false
		}
	}
	return true
}

// SetupWithManager registers the controller and its watches. The Deployment
// watch makes reconciliation event-driven; the periodic requeue during
// progression covers missed events.
func (r *AetherClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AetherCluster{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

// ownedBy reports whether the Deployment is controlled by the given
// AetherCluster.
func ownedBy(deploy *appsv1.Deployment, cr *v1alpha1.AetherCluster) bool {
	ref := metav1.GetControllerOf(deploy)
	return ref != nil && ref.UID == cr.UID
}

// orNil returns the object, or nil when the pointer is nil.
func orNil(d *appsv1.Deployment) *appsv1.Deployment {
	if d == nil || d.Name == "" {
		return nil
	}
	return d
}
