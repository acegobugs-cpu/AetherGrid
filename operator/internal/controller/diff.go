package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// deploymentDiff describes how the observed Deployment differs from the
// desired state. Only owned fields are compared; resourceVersion, UID,
// timestamps, managedFields, status and user-added labels/annotations are
// deliberately ignored.
type deploymentDiff struct {
	// needsUpdate indicates owned fields must be written back.
	needsUpdate bool
	// needsRecreate indicates the Deployment cannot be updated in place
	// (the immutable selector changed) and must be replaced.
	needsRecreate bool
	// reasons lists the human-readable drift causes.
	reasons []string
}

// compareDeployment detects drift between the desired and the observed
// Deployment.
func compareDeployment(desired, actual *appsv1.Deployment) deploymentDiff {
	var diff deploymentDiff

	if actual.Spec.Replicas == nil || *actual.Spec.Replicas != *desired.Spec.Replicas {
		diff.needsUpdate = true
		diff.reasons = append(diff.reasons, "replicas")
	}

	if actual.Spec.Selector == nil || !equality.Semantic.DeepEqual(
		actual.Spec.Selector.MatchLabels, desired.Spec.Selector.MatchLabels) {
		diff.needsRecreate = true
		diff.reasons = append(diff.reasons, "selector")
	}

	for k, v := range desired.Labels {
		if actual.Labels[k] != v {
			diff.needsUpdate = true
			diff.reasons = append(diff.reasons, "labels")
			break
		}
	}

	for k, v := range desired.Spec.Template.Labels {
		if actual.Spec.Template.Labels[k] != v {
			diff.needsUpdate = true
			diff.reasons = append(diff.reasons, "pod labels")
			break
		}
	}

	desiredContainer := desired.Spec.Template.Spec.Containers[0]
	actualContainers := actual.Spec.Template.Spec.Containers
	var actualContainer *corev1.Container
	for i := range actualContainers {
		if actualContainers[i].Name == desiredContainer.Name {
			actualContainer = &actualContainers[i]
			break
		}
	}
	if actualContainer == nil {
		diff.needsUpdate = true
		diff.reasons = append(diff.reasons, "container name")
	} else {
		if actualContainer.Image != desiredContainer.Image {
			diff.needsUpdate = true
			diff.reasons = append(diff.reasons, "image")
		}
		if !equality.Semantic.DeepEqual(actualContainer.Ports, desiredContainer.Ports) {
			diff.needsUpdate = true
			diff.reasons = append(diff.reasons, "ports")
		}
	}

	return diff
}

// applyDesired writes the owned fields of desired onto actual, preserving all
// other fields (user labels/annotations, status, resourceVersion, ...). It is
// only called when drift was detected.
func applyDesired(actual, desired *appsv1.Deployment) {
	actual.Spec.Replicas = desired.Spec.Replicas

	for k, v := range desired.Labels {
		if actual.Labels == nil {
			actual.Labels = map[string]string{}
		}
		actual.Labels[k] = v
	}
	for k, v := range desired.Spec.Template.Labels {
		if actual.Spec.Template.Labels == nil {
			actual.Spec.Template.Labels = map[string]string{}
		}
		actual.Spec.Template.Labels[k] = v
	}

	if len(actual.Spec.Template.Spec.Containers) > 0 {
		actual.Spec.Template.Spec.Containers[0] = desired.Spec.Template.Spec.Containers[0]
	} else {
		actual.Spec.Template.Spec.Containers = desired.Spec.Template.Spec.Containers
	}
}
