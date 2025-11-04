package doctor

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func Test_getNamespaces(t *testing.T) {
	objs := []runtime.Object{
		&v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "ns1",
			},
		},
		&v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "ns2",
			},
		},
		&corev1.ConfigMap{ // should not have its namespace listed (not a cluster)
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "ns3",
			},
		},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-doctor-report")

	namespaces, err := getNamespaces(ctx, fakeDynClient, logger)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedNS := map[string]struct{}{
		"default":                   {},
		"kube-system":               {},
		"cattle-fleet-system":       {},
		"cattle-fleet-local-system": {},
		"ns1":                       {},
		"ns2":                       {},
	}

	if len(namespaces) != len(expectedNS) {
		t.Fatalf("expected %d namespaces, got %d: %v", len(expectedNS), len(namespaces), namespaces)
	}

	for _, got := range namespaces {
		if _, ok := expectedNS[got]; !ok {
			t.Fatalf("got unexpected namespace %s", got)
		}
	}
}
