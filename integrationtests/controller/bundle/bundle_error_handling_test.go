package bundle

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	testFinalizer   = "test.fleet.cattle.io/block-deletion"
	testFinalizerNS = "test.fleet.cattle.io/block-ns-deletion"
)

var _ = Describe("Bundle controller error handling", Ordered, func() {
	var bundleNS string

	createNamespace := func(name string) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())
	}

	createCluster := func(name, namespace, statusNamespace string, labels map[string]string) *v1alpha1.Cluster {
		cluster := &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
			Spec:       v1alpha1.ClusterSpec{Paused: false},
		}
		Expect(k8sClient.Create(ctx, cluster)).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cluster)).To(Succeed())
			cluster.Status.Namespace = statusNamespace
			g.Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())
		}).Should(Succeed())

		return cluster
	}

	createBundle := func(name, namespace, defaultNS, configMapName string, targets []v1alpha1.BundleTarget) *v1alpha1.Bundle {
		bundle := &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{DefaultNamespace: defaultNS},
				Targets:                 targets,
				Resources: []v1alpha1.BundleResource{{
					Name:    "test.yaml",
					Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + configMapName + "\ndata:\n  key: value",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
		return bundle
	}

	getBundleDeployments := func(bundleName, bundleNS string) *v1alpha1.BundleDeploymentList {
		bdList := &v1alpha1.BundleDeploymentList{}
		Expect(k8sClient.List(ctx, bdList, client.MatchingLabelsSelector{
			Selector: labels.SelectorFromSet(map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": bundleNS,
			}),
		})).To(Succeed())
		return bdList
	}

	addFinalizer := func(obj client.Object, finalizer string) {
		Eventually(func(g Gomega) {
			key := client.ObjectKeyFromObject(obj)
			g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
			if !controllerutil.ContainsFinalizer(obj, finalizer) {
				controllerutil.AddFinalizer(obj, finalizer)
				g.Expect(k8sClient.Update(ctx, obj)).To(Succeed())
			}
		}).Should(Succeed())
	}

	removeFinalizer := func(obj client.Object, finalizer string) {
		Eventually(func(g Gomega) {
			key := client.ObjectKeyFromObject(obj)
			g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
			controllerutil.RemoveFinalizer(obj, finalizer)
			g.Expect(k8sClient.Update(ctx, obj)).To(Succeed())
		}).Should(Succeed())
	}

	updateBundleDefaultNamespace := func(bundle *v1alpha1.Bundle, newNS string) {
		Eventually(func(g Gomega) {
			latestBundle := &v1alpha1.Bundle{}
			key := client.ObjectKeyFromObject(bundle)
			g.Expect(k8sClient.Get(ctx, key, latestBundle)).To(Succeed())
			latestBundle.Spec.BundleDeploymentOptions.DefaultNamespace = newNS
			g.Expect(k8sClient.Update(ctx, latestBundle)).To(Succeed())
		}).Should(Succeed())
	}

	generateTestID := func() string {
		testID, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		if len(testID) > 5 {
			testID = testID[5:]
		}
		return testID
	}

	setupClusters := func(cluster1Name, cluster2Name, testID string, clusterLabels []map[string]string) (*v1alpha1.Cluster, *v1alpha1.Cluster) {
		cluster1NS := "cluster1-ns-" + testID
		cluster2NS := "cluster2-ns-" + testID

		createNamespace(cluster1NS)
		createNamespace(cluster2NS)

		cluster1 := createCluster(cluster1Name, bundleNS, cluster1NS, clusterLabels[0])
		cluster2 := createCluster(cluster2Name, bundleNS, cluster2NS, clusterLabels[1])

		return cluster1, cluster2
	}

	cleanupResources := func(bundle *v1alpha1.Bundle, bundleName, testID string, resources ...client.Object) {
		_ = k8sClient.Delete(ctx, bundle)
		Eventually(func() int {
			return len(getBundleDeployments(bundleName, bundleNS).Items)
		}).Should(Equal(0))

		for _, resource := range resources {
			_ = k8sClient.Delete(ctx, resource)
		}

		_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "cluster1-ns-" + testID}})
		_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "cluster2-ns-" + testID}})
	}

	BeforeAll(func() {
		var err error
		bundleNS, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		createNamespace(bundleNS)
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: bundleNS}})).ToNot(HaveOccurred())
		})
	})

	Context("Issue #4144 - UID tracking prevents incorrect deletion", func() {
		var (
			bundle     *v1alpha1.Bundle
			cluster1   *v1alpha1.Cluster
			cluster2   *v1alpha1.Cluster
			content    *v1alpha1.Content
			bundleName string
			testID     string
		)

		BeforeEach(func() {
			bundleName = "test-bundle-uid"
			testID = generateTestID()
			cluster1, cluster2 = setupClusters("cluster1", "cluster2", testID, []map[string]string{
				{"env": "test"},
				{"env": "test"},
			})

			content = &v1alpha1.Content{
				ObjectMeta: metav1.ObjectMeta{Name: "test-content-" + testID},
				Content:    []byte("test content"),
			}
			Expect(k8sClient.Create(ctx, content)).ToNot(HaveOccurred())

			bundle = createBundle(bundleName, bundleNS, "default", "test", []v1alpha1.BundleTarget{{
				ClusterSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"env": "test"}},
			}})
		})

		AfterEach(func() {
			cleanupResources(bundle, bundleName, testID, content, cluster1, cluster2)
		})

		It("should not delete existing bundledeployments when content resource fails", func() {
			var uid1, uid2 types.UID
			Eventually(func(g Gomega) {
				bdList := getBundleDeployments(bundleName, bundleNS)
				g.Expect(bdList.Items).To(HaveLen(2))
				uid1 = bdList.Items[0].UID
				uid2 = bdList.Items[1].UID
			}).Should(Succeed())

			addFinalizer(content, testFinalizer)
			Expect(k8sClient.Delete(ctx, content)).To(Succeed())

			Eventually(func(g Gomega) {
				c := &v1alpha1.Content{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: content.Name}, c)).To(Succeed())
				g.Expect(c.DeletionTimestamp).NotTo(BeNil())
			}).Should(Succeed())

			updateBundleDefaultNamespace(bundle, "kube-system")

			Consistently(func(g Gomega) {
				bdList := getBundleDeployments(bundleName, bundleNS)
				g.Expect(bdList.Items).To(HaveLen(2))
				currentUIDs := make(map[types.UID]bool)
				for _, bd := range bdList.Items {
					currentUIDs[bd.UID] = true
				}
				g.Expect(currentUIDs).To(HaveKey(uid1))
				g.Expect(currentUIDs).To(HaveKey(uid2))
			}, 5*time.Second, time.Second).Should(Succeed())

			removeFinalizer(content, testFinalizer)

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: content.Name}, &v1alpha1.Content{})
				g.Expect(err).To(HaveOccurred())
			}).Should(Succeed())
		})
	})

	Context("Issue #4028 - Continue processing all bundledeployments on error", func() {
		var (
			bundle     *v1alpha1.Bundle
			cluster1   *v1alpha1.Cluster
			cluster2   *v1alpha1.Cluster
			bundleName string
			testID     string
			cluster1NS string
			cluster2NS string
		)

		BeforeEach(func() {
			bundleName = "test-bundle-continue"
			testID = generateTestID()
			cluster1, cluster2 = setupClusters("cluster1-continue", "cluster2-continue", testID, []map[string]string{
				{"env": "test", "order": "1"},
				{"env": "test", "order": "2"},
			})
			cluster1NS = cluster1.Status.Namespace
			cluster2NS = cluster2.Status.Namespace

			bundle = createBundle(bundleName, bundleNS, "default", "test-continue", []v1alpha1.BundleTarget{{
				ClusterSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "test"},
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key:      "order",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"1", "2"},
					}},
				},
			}})
		})

		AfterEach(func() {
			cleanupResources(bundle, bundleName, testID, cluster1, cluster2)
		})

		It("should continue processing second bundledeployment when first fails", func() {
			var bd1UID, bd2UID types.UID
			Eventually(func(g Gomega) {
				bdList := getBundleDeployments(bundleName, bundleNS)
				g.Expect(bdList.Items).To(HaveLen(2))
				for _, bd := range bdList.Items {
					switch bd.Namespace {
					case cluster1NS:
						bd1UID = bd.UID
					case cluster2NS:
						bd2UID = bd.UID
					}
				}
				g.Expect(bd1UID).NotTo(BeEmpty())
				g.Expect(bd2UID).NotTo(BeEmpty())
			}).Should(Succeed())

			cluster1Namespace := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster1NS}, cluster1Namespace)).To(Succeed())
			addFinalizer(cluster1Namespace, testFinalizerNS)
			Expect(k8sClient.Delete(ctx, cluster1Namespace)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster1NS}, cluster1Namespace)).To(Succeed())
				g.Expect(cluster1Namespace.DeletionTimestamp).NotTo(BeNil())
			}).Should(Succeed())

			updateBundleDefaultNamespace(bundle, "kube-system")

			Eventually(func(g Gomega) {
				bdList := &v1alpha1.BundleDeploymentList{}
				g.Expect(k8sClient.List(ctx, bdList, client.InNamespace(cluster2NS))).To(Succeed())

				var bd2 *v1alpha1.BundleDeployment
				for _, bd := range bdList.Items {
					if bd.Labels["fleet.cattle.io/bundle-name"] == bundleName {
						bd2 = &bd
						break
					}
				}
				g.Expect(bd2).NotTo(BeNil())
				g.Expect(bd2.Spec.Options.DefaultNamespace).To(Equal("kube-system"))
			}).Should(Succeed())

			removeFinalizer(cluster1Namespace, testFinalizerNS)
		})
	})
})
