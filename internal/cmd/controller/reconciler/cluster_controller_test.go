package reconciler

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type FakeQuery struct {
}

// BundlesForCluster returns empty list, so no cleanup is needed
func (q *FakeQuery) BundlesForCluster(context.Context, *fleet.Cluster) ([]*fleet.Bundle, []*fleet.Bundle, error) {
	return nil, nil, nil
}

var _ = Describe("ClusterReconciler", func() {
	var (
		ctx        context.Context
		reconciler *ClusterReconciler
		k8sclient  client.Client
		cluster    *fleet.Cluster
		req        reconcile.Request
		sch        *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		sch = scheme.Scheme
		Expect(fleet.AddToScheme(sch)).To(Succeed())
		Expect(corev1.AddToScheme(sch)).To(Succeed())

		cluster = &fleet.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "fleet-local",
			},
			Status: fleet.ClusterStatus{
				Namespace: "cluster-test-cluster-somehash",
			},
		}

		req = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-cluster",
				Namespace: "fleet-local",
			},
		}
	})

	JustBeforeEach(func() {
		if k8sclient == nil {
			k8sclient = fake.NewClientBuilder().
				WithScheme(sch).
				WithObjects(cluster).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()
		}

		reconciler = &ClusterReconciler{
			Client: k8sclient,
			Scheme: sch,
			Query:  &FakeQuery{},
		}
	})

	AfterEach(func() {
		k8sclient = nil
	})

	Context("Reconcile finalizer", func() {
		It("should add a finalizer to a new cluster", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updatedCluster := &fleet.Cluster{}
			err = k8sclient.Get(ctx, req.NamespacedName, updatedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedCluster.Finalizers).To(ContainElement(finalize.ClusterFinalizer))
		})
	})

	Context("Reconcile deletion", func() {
		var clusterNamespace *corev1.Namespace

		BeforeEach(func() {
			cluster.Finalizers = []string{finalize.ClusterFinalizer}
			now := metav1.NewTime(time.Now())
			cluster.DeletionTimestamp = &now

			clusterNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: cluster.Status.Namespace,
				},
			}
		})

		JustBeforeEach(func() {
			k8sclient = fake.NewClientBuilder().
				WithScheme(sch).
				WithObjects(cluster, clusterNamespace).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()

			reconciler.Client = k8sclient
		})

		It("should delete the cluster namespace and remove the finalizer", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check finalizer is removed
			err = k8sclient.Get(ctx, req.NamespacedName, cluster)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue(), "cluster should be gone as finalizer is removed")

			// Check namespace is deleted
			ns := &corev1.Namespace{}
			err = k8sclient.Get(ctx, client.ObjectKey{Name: clusterNamespace.Name}, ns)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue(), "cluster namespace should be deleted")
		})

		It("should remove the finalizer when cluster namespace is not set", func() {
			// Remove cluster namespace before test
			cluster.Status.Namespace = ""
			k8sclient = fake.NewClientBuilder().
				WithScheme(sch).
				WithObjects(cluster).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()
			reconciler.Client = k8sclient

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check finalizer is removed
			err = k8sclient.Get(ctx, req.NamespacedName, cluster)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue(), "cluster should be gone as finalizer is removed")
		})

		It("should remove the finalizer even if the namespace is already gone", func() {
			// Delete the namespace before the test
			Expect(k8sclient.Delete(ctx, clusterNamespace)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check finalizer is removed
			err = k8sclient.Get(ctx, req.NamespacedName, cluster)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue(), "cluster should be gone as finalizer is removed")
		})
	})
})
