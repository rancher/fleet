package clusterregistration

import (
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sanitizeClusterLabels", func() {
	var request *fleet.ClusterRegistration

	BeforeEach(func() {
		request = &fleet.ClusterRegistration{}
		request.Namespace = "fleet-default"
		request.Name = "request-abc"
	})

	It("drops the spoofable management.cattle.io/cluster-display-name label", func() {
		request.Spec.ClusterLabels = map[string]string{
			"management.cattle.io/cluster-display-name": "not-allowed",
			"env": "prod",
		}

		labels := sanitizeClusterLabels(request)

		Expect(labels).ToNot(HaveKey("management.cattle.io/cluster-display-name"))
		// Non-reserved operational labels are preserved.
		Expect(labels).To(HaveKeyWithValue("env", "prod"))
	})

	It("drops arbitrary reserved management.cattle.io/* and fleet.cattle.io/* labels", func() {
		request.Spec.ClusterLabels = map[string]string{
			"management.cattle.io/anything": "x",
			"fleet.cattle.io/cluster":       "not-allowed",
			"team":                          "payments",
		}

		labels := sanitizeClusterLabels(request)

		Expect(labels).ToNot(HaveKey("management.cattle.io/anything"))
		Expect(labels).ToNot(HaveKey("fleet.cattle.io/cluster"))
		Expect(labels).To(HaveKeyWithValue("team", "payments"))
	})

	It("preserves the allow-listed fleet.cattle.io/created-by-agent-pod debug label", func() {
		request.Spec.ClusterLabels = map[string]string{
			"fleet.cattle.io/created-by-agent-pod": "agent-pod-xyz",
		}

		labels := sanitizeClusterLabels(request)

		Expect(labels).To(HaveKeyWithValue("fleet.cattle.io/created-by-agent-pod", "agent-pod-xyz"))
	})

	It("returns an empty, non-nil map when there are no labels", func() {
		labels := sanitizeClusterLabels(request)

		Expect(labels).ToNot(BeNil())
		Expect(labels).To(BeEmpty())
	})
})

var _ = Describe("isReservedLabel", func() {
	DescribeTable("classifying label keys",
		func(key string, reserved bool) {
			Expect(isReservedLabel(key)).To(Equal(reserved))
		},
		Entry("management.cattle.io display name is reserved", "management.cattle.io/cluster-display-name", true),
		Entry("any management.cattle.io key is reserved", "management.cattle.io/foo", true),
		Entry("any fleet.cattle.io key is reserved", "fleet.cattle.io/cluster", true),
		Entry("created-by-agent-pod is allow-listed", "fleet.cattle.io/created-by-agent-pod", false),
		Entry("plain operational label is not reserved", "env", false),
		Entry("unrelated vendor label is not reserved", "example.com/team", false),
	)
})

var _ = Describe("createOrGetCluster label sanitization", func() {
	var (
		request      *fleet.ClusterRegistration
		clusterCache *fake.MockCacheInterface[*fleet.Cluster]
		clusters     *fake.MockClientInterface[*fleet.Cluster, *fleet.ClusterList]
		h            *handler

		// The Cluster the handler passed to Create, captured for assertions.
		created  *fleet.Cluster
		notFound = apierrors.NewNotFound(schema.GroupResource{}, "")
	)

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		clusterCache = fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
		clusters = fake.NewMockClientInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
		h = &handler{clusterCache: clusterCache, clusters: clusters}
		created = nil

		request = &fleet.ClusterRegistration{
			ObjectMeta: metav1.ObjectMeta{Name: "request-abc", Namespace: "fleet-default"},
			Spec:       fleet.ClusterRegistrationSpec{ClientID: "chosen-id"},
		}

		// No cluster exists for this ClientID, and none under the derived name,
		// so registration takes the create-new branch that trusts agent labels.
		clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return(nil, nil)
		clusterCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, notFound)

		clusters.EXPECT().Create(gomock.Any()).DoAndReturn(
			func(c *fleet.Cluster) (*fleet.Cluster, error) {
				created = c
				return c, nil
			},
		)
	})

	It("does not persist a spoofed display-name label onto the new Cluster", func() {
		request.Spec.ClusterLabels = map[string]string{
			"management.cattle.io/cluster-display-name": "now-allowed",
			"env":  "prod",
			"team": "payments",
		}

		_, err := h.createOrGetCluster(request)
		Expect(err).ToNot(HaveOccurred())

		Expect(created).ToNot(BeNil())
		Expect(created.Labels).ToNot(HaveKey("management.cattle.io/cluster-display-name"))
		// Legitimate targeting labels still reach the Cluster.
		Expect(created.Labels).To(HaveKeyWithValue("env", "prod"))
		Expect(created.Labels).To(HaveKeyWithValue("team", "payments"))
	})

	It("overwrites an agent-injected fleet.cattle.io/cluster label with the trusted cluster name", func() {
		request.Spec.ClusterLabels = map[string]string{
			fleet.ClusterAnnotation: "some-other-cluster",
		}

		_, err := h.createOrGetCluster(request)
		Expect(err).ToNot(HaveOccurred())

		expected := names.SafeConcatName("cluster", names.KeyHash(request.Spec.ClientID))
		Expect(created.Name).To(Equal(expected))
		Expect(created.Labels).To(HaveKeyWithValue(fleet.ClusterAnnotation, expected))
	})

	It("keeps the allow-listed created-by-agent-pod label on the new Cluster", func() {
		request.Spec.ClusterLabels = map[string]string{
			fleet.CreatedByAgentPodLabel: "agent-pod-xyz",
			"fleet.cattle.io/spoofed":    "nope",
		}

		_, err := h.createOrGetCluster(request)
		Expect(err).ToNot(HaveOccurred())

		Expect(created.Labels).To(HaveKeyWithValue(fleet.CreatedByAgentPodLabel, "agent-pod-xyz"))
		Expect(created.Labels).ToNot(HaveKey("fleet.cattle.io/spoofed"))
	})
})
