package agent

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	v1gen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var (
	env                        *specEnv
	bundleController           v1gen.BundleController
	clusterController          v1gen.ClusterController
	bundleDeploymentController v1gen.BundleDeploymentController
	clusterGroupController     v1gen.ClusterGroupController
)

var _ = Describe("Bundle targets", Ordered, func() {
	BeforeAll(func() {
		env = specEnvs["targets"]
		bundleController = env.fleet.V1alpha1().Bundle()
		clusterController = env.fleet.V1alpha1().Cluster()
		bundleDeploymentController = env.fleet.V1alpha1().BundleDeployment()
		clusterGroupController = env.fleet.V1alpha1().ClusterGroup()

		createClustersAndClusterGroups()

		DeferCleanup(func() {
			Expect(env.k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.namespace}})).ToNot(HaveOccurred())
		})
	})

	var (
		targets            []v1alpha1.BundleTarget
		targetRestrictions []v1alpha1.BundleTarget
		bundleName         string
		bdLabels           map[string]string
	)

	JustBeforeEach(func() {
		bundle, err := createBundle(bundleName, env.namespace, bundleController, targets, targetRestrictions)
		Expect(err).NotTo(HaveOccurred())
		Expect(bundle).To(Not(BeNil()))
	})

	AfterEach(func() {
		Expect(bundleController.Delete(env.namespace, bundleName, nil)).NotTo(HaveOccurred())
	})

	When("target all clusters without customization", func() {
		BeforeEach(func() {
			bundleName = "all"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": env.namespace,
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targets))
			copy(targetRestrictions, targets)
		})

		It("three BundleDeployments are created", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(3, bdLabels)
			By("and BundleDeployments don't have values from customizations")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values).To(BeNil())
			}
		})
	})

	When("target customization for all clusters", func() {
		BeforeEach(func() {
			bundleName = "all-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": env.namespace,
			}
			// simulate targets in fleet.yaml which are used for customization
			targets = []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "3"}},
						},
					},
					ClusterGroup: "all",
				},
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targetsInGitRepo := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "1"}},
						},
					},
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targetsInGitRepo))
			copy(targetRestrictions, targetsInGitRepo)
			targets = append(targets, targetsInGitRepo...)
		})

		It("three BundleDeployments are created", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(3, bdLabels)
			By("and BundleDeployments have values from customizations")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values.Data).To(Equal(map[string]interface{}{"replicas": "3"}))
			}
		})
	})

	When("target customization for clusters one and two", func() {
		BeforeEach(func() {
			bundleName = "one-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": env.namespace,
			}
			// simulate targets in fleet.yaml which are used for customization
			targets = []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "1"}},
						},
					},
					ClusterGroup: "one",
				},
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "2"}},
						},
					},
					ClusterGroup: "two",
				},
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targetsInGitRepo := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "4"}},
						},
					},
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targetsInGitRepo))
			copy(targetRestrictions, targetsInGitRepo)
			targets = append(targets, targetsInGitRepo...)
		})

		It("three BundleDeployments are created", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(3, bdLabels)
			By("and just BundleDeployment from cluster one and two are customized")
			for _, bd := range bdList.Items {
				if strings.Contains(bd.ObjectMeta.Namespace, "cluster-one") {
					Expect(bd.Spec.Options.Helm.Values.Data).To(Equal(map[string]interface{}{"replicas": "1"}))
				} else if strings.Contains(bd.ObjectMeta.Namespace, "cluster-two") {
					Expect(bd.Spec.Options.Helm.Values.Data).To(Equal(map[string]interface{}{"replicas": "2"}))
				} else {
					Expect(bd.Spec.Options.Helm.Values.Data).To(Equal(map[string]interface{}{"replicas": "4"}))
				}
			}
		})
	})

	When("target customization for cluster one and all clusters", func() {
		BeforeEach(func() {
			bundleName = "one-all-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": env.namespace,
			}
			// simulate targets in fleet.yaml which are used for customization
			targets = []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "1"}},
						},
					},
					ClusterGroup: "one",
				},
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "4"}},
						},
					},
					ClusterGroup: "all",
				},
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targetsInGitRepo := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "5"}},
						},
					},
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targetsInGitRepo))
			copy(targetRestrictions, targetsInGitRepo)
			targets = append(targets, targetsInGitRepo...)
		})

		It("three BundleDeployments are created", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(3, bdLabels)
			By("and just BundleDeployment from cluster one is customized")
			for _, bd := range bdList.Items {
				if strings.Contains(bd.ObjectMeta.Namespace, "cluster-one") {
					Expect(bd.Spec.Options.Helm.Values.Data).To(Equal(map[string]interface{}{"replicas": "1"}))
				} else {
					Expect(bd.Spec.Options.Helm.Values.Data).To(Equal(map[string]interface{}{"replicas": "4"}))
				}
			}
		})
	})

	When("target customization for all cluster when targeting just cluster one", func() {
		BeforeEach(func() {
			bundleName = "one-target-all-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": env.namespace,
			}
			// simulate targets in fleet.yaml which are used for customization
			targets = []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "2"}},
						},
					},
					ClusterGroup: "all",
				},
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targetsInGitRepo := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Values: &v1alpha1.GenericMap{Data: map[string]interface{}{"replicas": "1"}},
						},
					},
					ClusterGroup: "one",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targetsInGitRepo))
			copy(targetRestrictions, targetsInGitRepo)
			targets = append(targets, targetsInGitRepo...)
		})

		It("one BundleDeployment is created", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(1, bdLabels)
			By("and the BundleDeployment is customized")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values.Data).To(Equal(map[string]interface{}{"replicas": "2"}))
			}
		})
	})
})

func verifyBundlesDeploymentsAreCreated(numBundleDeployments int, bdLabels map[string]string) *v1alpha1.BundleDeploymentList {
	var bdList *v1alpha1.BundleDeploymentList
	var err error
	Eventually(func() int {
		bdList, err = bundleDeploymentController.List("", metav1.ListOptions{LabelSelector: labels.SelectorFromSet(bdLabels).String()})
		Expect(err).NotTo(HaveOccurred())

		return len(bdList.Items)
	}).Should(Equal(numBundleDeployments))

	return bdList
}

// creates:
// - three clusters
// - four cluster groups: one per cluster and another that matches all clusters
// - a namespace per cluster. This is to simulate the namespace created for placing the BundleDeployments, this
// is done by another controller, then it is set in the status field.
func createClustersAndClusterGroups() {
	clusterNs, err := utils.NewNamespaceName()
	Expect(err).ToNot(HaveOccurred())
	clusterNs = clusterNs + "cluster-one"
	Expect(env.k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterNs,
		},
	})).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(env.k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNs}})).ToNot(HaveOccurred())
	})

	clusterOne, err := createCluster("one", env.namespace, clusterController, map[string]string{"cluster": "one", "env": "test"}, clusterNs)
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterOne).To(Not(BeNil()))

	clusterNs, err = utils.NewNamespaceName()
	Expect(err).ToNot(HaveOccurred())
	clusterNs = clusterNs + "cluster-two"
	Expect(env.k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterNs,
		},
	})).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(env.k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNs}})).ToNot(HaveOccurred())
	})
	clusterTwo, err := createCluster("two", env.namespace, clusterController, map[string]string{"cluster": "two", "env": "test"}, clusterNs)
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterTwo).To(Not(BeNil()))

	clusterNs, err = utils.NewNamespaceName()
	Expect(err).ToNot(HaveOccurred())
	clusterNs = clusterNs + "cluster-three"
	Expect(env.k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterNs,
		},
	})).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(env.k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNs}})).ToNot(HaveOccurred())
	})
	clusterThree, err := createCluster("three", env.namespace, clusterController, map[string]string{"cluster": "three", "env": "test"}, clusterNs)
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterThree).To(Not(BeNil()))

	clusterGroupOne, err := createClusterGroup("one", env.namespace, clusterGroupController, &metav1.LabelSelector{
		MatchLabels: map[string]string{"cluster": "one"},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterGroupOne).To(Not(BeNil()))

	clusterGroupTwo, err := createClusterGroup("two", env.namespace, clusterGroupController, &metav1.LabelSelector{
		MatchLabels: map[string]string{"cluster": "two"},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterGroupTwo).To(Not(BeNil()))

	clusterGroupThree, err := createClusterGroup("three", env.namespace, clusterGroupController, &metav1.LabelSelector{
		MatchLabels: map[string]string{"cluster": "three"},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterGroupThree).To(Not(BeNil()))

	clusterGroupAll, err := createClusterGroup("all", env.namespace, clusterGroupController, &metav1.LabelSelector{
		MatchLabels: map[string]string{"env": "test"},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterGroupAll).To(Not(BeNil()))
}
