package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/ptr"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
)

func deploymentForTest(image string, replicas int32) *appsv1.Deployment {
	cr := testCluster("example", func(cr *v1alpha1.AetherCluster) {
		cr.Spec.Image = image
		cr.Spec.Replicas = ptr.To(replicas)
	})
	deploy, err := buildDeployment(cr, testScheme())
	if err != nil {
		panic(err)
	}
	return deploy
}

func TestCompareDeploymentNoDrift(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := desired.DeepCopy()
	diff := compareDeployment(desired, actual)
	if diff.needsUpdate || diff.needsRecreate {
		t.Fatalf("expected no drift, got %+v", diff)
	}
}

func TestCompareDeploymentIgnoresForeignFields(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := desired.DeepCopy()
	actual.ResourceVersion = "999"
	actual.UID = "deadbeef"
	actual.Labels["user-label"] = "keep-me"
	actual.Spec.Template.Spec.Containers[0].ImagePullPolicy = "IfNotPresent"
	actual.Status.ReadyReplicas = 2
	actual.Spec.RevisionHistoryLimit = ptr.To(int32(10))

	diff := compareDeployment(desired, actual)
	if diff.needsUpdate || diff.needsRecreate {
		t.Fatalf("expected no drift from foreign fields, got %+v", diff)
	}
}

func TestCompareDeploymentReplicaDrift(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := desired.DeepCopy()
	actual.Spec.Replicas = ptr.To(int32(1))
	diff := compareDeployment(desired, actual)
	if !diff.needsUpdate || diff.needsRecreate {
		t.Fatalf("expected replica drift (update), got %+v", diff)
	}
}

func TestCompareDeploymentImageDrift(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := deploymentForTest("nginx:latest", 2)
	diff := compareDeployment(desired, actual)
	if !diff.needsUpdate || diff.needsRecreate {
		t.Fatalf("expected image drift (update), got %+v", diff)
	}
}

func TestCompareDeploymentContainerNameDrift(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := desired.DeepCopy()
	actual.Spec.Template.Spec.Containers[0].Name = "renamed"
	diff := compareDeployment(desired, actual)
	if !diff.needsUpdate || diff.needsRecreate {
		t.Fatalf("expected container name drift (update), got %+v", diff)
	}
}

func TestCompareDeploymentLabelDrift(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := desired.DeepCopy()
	delete(actual.Labels, LabelManaged)
	diff := compareDeployment(desired, actual)
	if !diff.needsUpdate {
		t.Fatalf("expected label drift (update), got %+v", diff)
	}
}

func TestCompareDeploymentSelectorDrift(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := desired.DeepCopy()
	actual.Spec.Selector.MatchLabels[LabelName] = "different"
	diff := compareDeployment(desired, actual)
	if !diff.needsRecreate {
		t.Fatalf("expected selector drift (recreate), got %+v", diff)
	}
}

func TestApplyDesiredPreservesForeignFields(t *testing.T) {
	desired := deploymentForTest("nginx:stable", 2)
	actual := desired.DeepCopy()
	actual.Spec.Replicas = ptr.To(int32(1))
	actual.Spec.Template.Spec.Containers[0].Image = "nginx:latest"
	actual.Labels["user-label"] = "keep-me"
	actual.Status.ReadyReplicas = 1
	actual.ResourceVersion = "42"

	applyDesired(actual, desired)

	if *actual.Spec.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", *actual.Spec.Replicas)
	}
	if got := actual.Spec.Template.Spec.Containers[0].Image; got != "nginx:stable" {
		t.Errorf("image = %q, want nginx:stable", got)
	}
	if actual.Labels["user-label"] != "keep-me" {
		t.Error("user label was removed")
	}
	if actual.Status.ReadyReplicas != 1 {
		t.Error("status was overwritten")
	}
	if actual.ResourceVersion != "42" {
		t.Error("resourceVersion was overwritten")
	}
}
