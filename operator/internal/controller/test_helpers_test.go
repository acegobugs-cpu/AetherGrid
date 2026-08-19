package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"github.com/acegobugs-cpu/AetherGrid/operator/api/v1alpha1"
)

// testScheme builds a scheme able to handle the API types used in tests.
func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		panic(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		panic(err)
	}
	return s
}

// testCluster builds an AetherCluster fixture.
func testCluster(name string, mutate func(*v1alpha1.AetherCluster)) *v1alpha1.AetherCluster {
	cr := &v1alpha1.AetherCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "AetherCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "aether-grid-test",
			UID:        "11111111-2222-3333-4444-555555555555",
			Generation: 1,
		},
		Spec: v1alpha1.AetherClusterSpec{
			Replicas: ptr.To(int32(2)),
			Image:    "nginx:stable",
		},
	}
	if mutate != nil {
		mutate(cr)
	}
	return cr
}
