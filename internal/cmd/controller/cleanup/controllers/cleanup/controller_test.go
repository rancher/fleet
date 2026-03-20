package cleanup

import (
	"testing"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCleanup(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cleanup Controller Suite")
}

var _ = Describe("cleanupNamespace", func() {
	var (
		mockCtrl        *gomock.Controller
		clusterCache    *fake.MockCacheInterface[*fleet.Cluster]
		clusterClient   *fake.MockClientInterface[*fleet.Cluster, *fleet.ClusterList]
		namespaceClient *fake.MockNonNamespacedClientInterface[*corev1.Namespace, *corev1.NamespaceList]
		h               *handler
		ns              *corev1.Namespace
		notFound        = apierrors.NewNotFound(schema.GroupResource{Group: "fleet.cattle.io", Resource: "clusters"}, "test-cluster")
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		clusterCache = fake.NewMockCacheInterface[*fleet.Cluster](mockCtrl)
		clusterClient = fake.NewMockClientInterface[*fleet.Cluster, *fleet.ClusterList](mockCtrl)
		namespaceClient = fake.NewMockNonNamespacedClientInterface[*corev1.Namespace, *corev1.NamespaceList](mockCtrl)

		h = &handler{
			clusters:       clusterCache,
			clustersClient: clusterClient,
			namespaces:     namespaceClient,
		}

		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster-fleet-default-test-cluster-abc123",
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
				Annotations: map[string]string{
					fleet.ClusterAnnotation:          "test-cluster",
					fleet.ClusterNamespaceAnnotation: "fleet-default",
				},
			},
		}
	})

	Context("when namespace has no managed label", func() {
		It("does not delete the namespace", func() {
			ns.Labels = map[string]string{}
			result, err := h.cleanupNamespace(ns.Name, ns)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ns))
		})
	})

	Context("when namespace is nil", func() {
		It("returns nil without error", func() {
			result, err := h.cleanupNamespace("some-key", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
		})
	})

	Context("when cluster exists in cache", func() {
		It("does not delete the namespace", func() {
			clusterCache.EXPECT().
				Get("fleet-default", "test-cluster").
				Return(&fleet.Cluster{}, nil)

			result, err := h.cleanupNamespace(ns.Name, ns)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ns))
		})
	})

	Context("when cluster is missing from cache but exists in API server", func() {
		// This tests the fix for the race condition described in
		// https://github.com/rancher/fleet/issues/3830: the cleanup
		// controller's informer cache may not yet reflect a newly created
		// Cluster, so a not-found cache result is confirmed via a live
		// API call before the namespace is deleted.
		It("does not delete the namespace", func() {
			// Cache says cluster not found (stale)
			clusterCache.EXPECT().
				Get("fleet-default", "test-cluster").
				Return(nil, notFound)

			// API server confirms cluster exists
			clusterClient.EXPECT().
				Get("fleet-default", "test-cluster", gomock.Any()).
				Return(&fleet.Cluster{}, nil)

			result, err := h.cleanupNamespace(ns.Name, ns)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ns))
		})
	})

	Context("when cluster is missing from both cache and API server", func() {
		It("deletes the namespace", func() {
			clusterCache.EXPECT().
				Get("fleet-default", "test-cluster").
				Return(nil, notFound)

			clusterClient.EXPECT().
				Get("fleet-default", "test-cluster", gomock.Any()).
				Return(nil, notFound)

			namespaceClient.EXPECT().
				Delete(ns.Name, gomock.Any()).
				Return(nil)

			result, err := h.cleanupNamespace(ns.Name, ns)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ns))
		})
	})

	Context("when cache lookup returns a non-NotFound error", func() {
		It("returns the error without deleting", func() {
			apiErr := apierrors.NewServiceUnavailable("API server unavailable")
			clusterCache.EXPECT().
				Get("fleet-default", "test-cluster").
				Return(nil, apiErr)

			result, err := h.cleanupNamespace(ns.Name, ns)
			Expect(err).To(Equal(apiErr))
			Expect(result).To(Equal(ns))
		})
	})

	Context("when API server lookup returns a non-NotFound error", func() {
		It("returns the error without deleting", func() {
			apiErr := apierrors.NewServiceUnavailable("API server unavailable")
			clusterCache.EXPECT().
				Get("fleet-default", "test-cluster").
				Return(nil, notFound)

			clusterClient.EXPECT().
				Get("fleet-default", "test-cluster", gomock.Any()).
				Return(nil, apiErr)

			result, err := h.cleanupNamespace(ns.Name, ns)
			Expect(err).To(Equal(apiErr))
			Expect(result).To(Equal(ns))
		})
	})
})
