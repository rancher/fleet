package agentmanagement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("cluster namespace lifecycle", func() {
	var regNamespace string

	BeforeEach(func() {
		ns := newGeneratedNamespace("cluster-controller-test-")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		regNamespace = ns.Name
	})

	It("sets status.Namespace to the documented hash and creates the namespace with the managed label and annotations", func() {
		cluster := newCluster(regNamespace, "test-cluster")
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		expectedNS := clusterNamespaceName(regNamespace, "test-cluster")
		Eventually(func(g Gomega) {
			c := &fleet.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: "test-cluster"}, c)).To(Succeed())
			g.Expect(c.Status.Namespace).To(Equal(expectedNS))
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			ns := &corev1.Namespace{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, ns)).To(Succeed())
			g.Expect(ns.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			g.Expect(ns.Annotations).To(HaveKeyWithValue(fleet.ClusterNamespaceAnnotation, regNamespace))
			g.Expect(ns.Annotations).To(HaveKeyWithValue(fleet.ClusterAnnotation, "test-cluster"))
		}).Should(Succeed())
	})

	It("does not create a namespace for a cluster carrying a custom cluster-management label", func() {
		cluster := newCluster(regNamespace, "skip-cluster")
		cluster.Labels = map[string]string{fleet.ClusterManagementLabel: "custom-manager"}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		Eventually(func(g Gomega) {
			c := &fleet.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: "skip-cluster"}, c)).To(Succeed())
			g.Expect(c.Status.Conditions).To(ContainElement(HaveField("Type", fleet.ClusterConditionProcessed)))
		}).Should(Succeed())

		expectedNS := clusterNamespaceName(regNamespace, "skip-cluster")
		Consistently(func(g Gomega) {
			c := &fleet.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: "skip-cluster"}, c)).To(Succeed())
			g.Expect(c.Status.Namespace).To(BeEmpty())

			err := k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, &corev1.Namespace{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}).Should(Succeed())
	})

	It("does not overwrite a namespace that already exists at the generated name", func() {
		clusterName := "preexisting-ns-cluster"
		expectedNS := clusterNamespaceName(regNamespace, clusterName)

		preexisting := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   expectedNS,
				Labels: map[string]string{"owner": "someone-else"},
			},
		}
		Expect(k8sClient.Create(ctx, preexisting)).To(Succeed())

		cluster := newCluster(regNamespace, clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		Eventually(func(g Gomega) {
			c := &fleet.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: clusterName}, c)).To(Succeed())
			g.Expect(c.Status.Namespace).To(Equal(expectedNS))
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			ns := &corev1.Namespace{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, ns)).To(Succeed())
			g.Expect(ns.Labels).To(HaveKeyWithValue("owner", "someone-else"))
			g.Expect(ns.Labels).NotTo(HaveKey(fleet.ManagedLabel))
		}).Should(Succeed())
	})

	It("does not delete or modify the generated namespace itself when the owning cluster is deleted", func() {
		clusterName := "deleted-cluster"
		cluster := newCluster(regNamespace, clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		expectedNS := clusterNamespaceName(regNamespace, clusterName)
		namespaceExists(expectedNS).Should(Succeed())

		Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: clusterName}, &fleet.Cluster{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, &corev1.Namespace{})).To(Succeed())
		}).Should(Succeed())
	})

	Describe("bundledeployment changes", func() {
		It("does not re-create a deleted cluster namespace when a BundleDeployment changes in a namespace without cluster annotations", func() {
			clusterName := "bd-no-requeue-cluster"
			expectedNS := clusterNamespaceName(regNamespace, clusterName)

			// Stand up a cluster and let its generated namespace appear, so
			// that a spurious enqueue of the cluster would have an observable
			// effect: the namespace coming back.
			cluster := newCluster(regNamespace, clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			Eventually(func(g Gomega) {
				c := &fleet.Cluster{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: clusterName}, c)).To(Succeed())
				g.Expect(c.Status.Namespace).To(
					Equal(expectedNS),
					"cluster status namespace does not match expected namespace",
				)
			}).Should(Succeed())

			namespaceExists(expectedNS).Should(Succeed())

			// Drop the generated namespace, so that only a further reconcile of
			// the cluster can bring it back. Deleting it does not enqueue the
			// cluster.
			deleteNamespace(expectedNS) // includes waiting for the namespace to be deleted.

			// The BundleDeployment lives in a namespace without cluster
			// annotations. The watch still fires, but the resolver must map it
			// to no cluster, so the deleted namespace stays gone.
			plainNS := newGeneratedNamespace("no-cluster-annotations-")
			Expect(k8sClient.Create(ctx, plainNS)).To(Succeed())

			bd := newBundleDeployment(plainNS.Name, "orphan-bd")
			Expect(k8sClient.Create(ctx, bd)).To(Succeed())

			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx,
					types.NamespacedName{Namespace: plainNS.Name, Name: "orphan-bd"},
					&fleet.BundleDeployment{})).To(Succeed())

				err := k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, &corev1.Namespace{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cluster namespace should be deleted and not re-created")
			}).Should(Succeed())
		})

		It("re-creates the cluster namespace when a BundleDeployment changes in a namespace with cluster annotations", func() {
			clusterName := "bd-requeue-cluster"
			expectedNS := clusterNamespaceName(regNamespace, clusterName)

			// A namespace annotated for the cluster, holding the BundleDeployment.
			// It is created first so the controller's namespace cache is warm by
			// the time the BundleDeployment triggers the watch.
			annotatedNS := newGeneratedNamespace("cluster-annotated-")
			annotatedNS.Annotations = map[string]string{
				fleet.ClusterNamespaceAnnotation: regNamespace,
				fleet.ClusterAnnotation:          clusterName,
			}
			Expect(k8sClient.Create(ctx, annotatedNS)).To(Succeed())

			cluster := newCluster(regNamespace, clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			Eventually(func(g Gomega) {
				c := &fleet.Cluster{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: clusterName}, c)).To(Succeed())
				g.Expect(c.Status.Namespace).To(
					Equal(expectedNS),
					"cluster status namespace does not match expected namespace",
				)
			}).Should(Succeed())

			namespaceExists(expectedNS).Should(Succeed())

			// Drop the generated namespace, so that only a further reconcile of the
			// cluster can bring it back. Deleting it does not enqueue the cluster.
			deleteNamespace(expectedNS) // includes waiting for the namespace to be deleted.
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, &corev1.Namespace{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cluster namespace should be deleted and not re-created")
			}).Should(Succeed())

			bd := newBundleDeployment(annotatedNS.Name, "requeue-bd")
			Expect(k8sClient.Create(ctx, bd)).To(Succeed())

			Eventually(func(g Gomega) {
				ns := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, ns)).To(Succeed())
				g.Expect(ns.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
				g.Expect(ns.Annotations).To(HaveKeyWithValue(fleet.ClusterNamespaceAnnotation, regNamespace))
				g.Expect(ns.Annotations).To(HaveKeyWithValue(fleet.ClusterAnnotation, clusterName))
			}).Should(Succeed())
		})

		It("does not corrupt the owning cluster's generated namespace or status when a BundleDeployment is created inside it", func() {
			clusterName := "bd-trigger-cluster"
			cluster := newCluster(regNamespace, clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			expectedNS := clusterNamespaceName(regNamespace, clusterName)

			Eventually(func(g Gomega) {
				c := &fleet.Cluster{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: clusterName}, c)).To(Succeed())
				g.Expect(c.Status.Namespace).To(
					Equal(expectedNS),
					"cluster status namespace does not match expected namespace before agent BD creation",
				)
			}).Should(Succeed())

			namespaceExists(expectedNS).Should(Succeed())

			bd := newBundleDeployment(expectedNS, "agent-bd")
			Expect(k8sClient.Create(ctx, bd)).To(Succeed())

			Consistently(func(g Gomega) {
				c := &fleet.Cluster{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: clusterName}, c)).To(Succeed())
				g.Expect(c.Status.Namespace).To(
					Equal(expectedNS),
					"cluster status namespace does not match expected namespace after agent BD creation",
				)

				ns := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: expectedNS}, ns)).To(Succeed())
				g.Expect(ns.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			}).Should(Succeed())
		})
	})
})
