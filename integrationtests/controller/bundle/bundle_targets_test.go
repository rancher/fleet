package bundle

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v2"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Bundle targets", Ordered, func() {
	BeforeAll(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		createClustersAndClusterGroups()

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	var (
		targets                           []v1alpha1.BundleTarget
		targetRestrictions                []v1alpha1.BundleTarget
		bundleName                        string
		bdLabels                          map[string]string
		expectedNumberOfBundleDeployments int
	)

	loadValues := func(bd v1alpha1.BundleDeployment) (map[string]any, map[string]any) {
		values := make(map[string]any)
		staged := make(map[string]any)
		secret := corev1.Secret{}
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}, &secret)
			g.Expect(err).ToNot(HaveOccurred())

			err = yaml.Unmarshal(secret.Data["stagedValues"], &staged)
			g.Expect(err).ToNot(HaveOccurred())

			err = yaml.Unmarshal(secret.Data["values"], &values)
			g.Expect(err).ToNot(HaveOccurred())
		}).Should(Succeed())

		return values, staged
	}

	JustBeforeEach(func() {
		bundle, err := utils.CreateBundle(ctx, k8sClient, bundleName, namespace, targets, targetRestrictions)
		Expect(err).NotTo(HaveOccurred())
		Expect(bundle).To(Not(BeNil()))
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(ctx, &v1alpha1.Bundle{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName}})).NotTo(HaveOccurred())
		bdList := &v1alpha1.BundleDeploymentList{}
		err := k8sClient.List(ctx, bdList, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(bdLabels)})
		Expect(err).NotTo(HaveOccurred())
		for _, bd := range bdList.Items {
			err := k8sClient.Delete(ctx, &bd)
			// BundleDeployments are now deleted in a loop by the controller, hence this delete operation
			// should not be necessary. Pending further tests, we choose to ignore errors indicating that the bundle
			// deployment has already been deleted here.
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		}
	})

	When("a GitRepo targets all clusters without customization", func() {
		BeforeEach(func() {
			bundleName = "all"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targets))
			copy(targetRestrictions, targets)
		})

		It("creates three BundleDeployments", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
			By("and BundleDeployments don't have values from customizations")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values).To(BeNil())
				err := k8sClient.Get(ctx, types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}, &corev1.Secret{})
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

			}
		})
	})

	When("a target customization is specified for all clusters", func() {
		BeforeEach(func() {
			bundleName = "all-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
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

		It("creates three BundleDeployments", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
			By("and BundleDeployments have values from customizations")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values).To(BeNil())
				values, _ := loadValues(bd)
				Expect(values).To(HaveKeyWithValue("replicas", "3"))
			}
		})
	})

	When("target customizations are specified for clusters one and two", func() {
		BeforeEach(func() {
			bundleName = "one-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
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

		It("creates three BundleDeployments", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
			By("and just BundleDeployment from cluster one and two are customized")
			for _, bd := range bdList.Items {
				values, _ := loadValues(bd)
				if strings.Contains(bd.Namespace, "cluster-one") {
					Expect(values).To(HaveKeyWithValue("replicas", "1"))
				} else if strings.Contains(bd.ObjectMeta.Namespace, "cluster-two") {
					Expect(values).To(HaveKeyWithValue("replicas", "2"))
				} else {
					Expect(values).To(HaveKeyWithValue("replicas", "4"))
				}
			}
		})
	})

	When("target customizations are specified both for cluster one, and for all clusters", func() {
		BeforeEach(func() {
			bundleName = "one-all-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
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

		It("creates three BundleDeployments", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
			By("and just BundleDeployment from cluster one is customized")
			for _, bd := range bdList.Items {
				values, _ := loadValues(bd)
				if strings.Contains(bd.Namespace, "cluster-one") {
					Expect(values).To(HaveKeyWithValue("replicas", "1"))
				} else {
					Expect(values).To(HaveKeyWithValue("replicas", "4"))
				}
			}
		})
	})

	When("target customizations are specified for all clusters but the GitRepo targets only cluster one", func() {
		BeforeEach(func() {
			bundleName = "one-target-all-customized"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 1
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

		It("creates one BundleDeployment", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
			By("and the BundleDeployment is customized")
			for _, bd := range bdList.Items {
				values, staged := loadValues(bd)
				Expect(staged).To(HaveKeyWithValue("replicas", "2"))
				Expect(values).To(HaveKeyWithValue("replicas", "2"))
			}
		})
	})

	// Bundles created without a GitRepo. It simulates how Rancher creates Bundles
	When("a Bundle does not contain any TargetRestrictions", func() {
		BeforeEach(func() {
			bundleName = "all"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, 0)
		})

		It("creates three BundleDeployments", func() {
			_ = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
		})
	})

	When("GitRepo has a target that matches clusterGroup all, and a targetCustomization that matches all clusters has DoNotDeploy set to true", func() {
		BeforeEach(func() {
			bundleName = "skip"
			expectedNumberOfBundleDeployments = 0
			// simulate targets in fleet.yaml which are used for customization
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
					DoNotDeploy:  true,
				},
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targetsInGitRepo := []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targetsInGitRepo))
			copy(targetRestrictions, targetsInGitRepo)
			targets = append(targets, targetsInGitRepo...)
		})

		It("no BundleDeployments are created", func() {
			waitForBundleToBeReady(bundleName)
			_ = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
		})
	})

	When("GitRepo has a target that matches clusterGroup one, and a targetCustomization that matches all clusters has DoNotDeploy set to true", func() {
		BeforeEach(func() {
			bundleName = "skipone"
			expectedNumberOfBundleDeployments = 0
			// simulate targets in fleet.yaml which are used for customization
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
					DoNotDeploy:  true,
				},
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targetsInGitRepo := []v1alpha1.BundleTarget{
				{
					ClusterGroup: "one",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targetsInGitRepo))
			copy(targetRestrictions, targetsInGitRepo)
			targets = append(targets, targetsInGitRepo...)
		})

		It("no BundleDeployments are created", func() {
			waitForBundleToBeReady(bundleName)
			_ = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
		})
	})

	When("GitRepo has a target that matches clusterGroup one, and a targetCustomization that matches clusterGroup two has DoNotDeploy set to true", func() {
		BeforeEach(func() {
			bundleName = "dontskip"
			expectedNumberOfBundleDeployments = 1
			// simulate targets in fleet.yaml which are used for customization
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "two",
					DoNotDeploy:  true,
				},
			}
			// simulate targets in GitRepo. All targets in GitRepo are also added to targetRestrictions, which acts as a white list
			targetsInGitRepo := []v1alpha1.BundleTarget{
				{
					ClusterGroup: "one",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targetsInGitRepo))
			copy(targetRestrictions, targetsInGitRepo)
			targets = append(targets, targetsInGitRepo...)
		})

		It("one BundleDeployment is created", func() {
			_ = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
		})
	})

	When("setting doNotDeploy to a target customization after the bundle has been deployed", func() {
		BeforeEach(func() {
			bundleName = "one-customized-do-not-deploy-two-later"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
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

		It("checks that bundle deployments for all three clusters exist and two is gone after setting do not deploy", func() {
			var bdList = verifyBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName)
			By("checking that all bundle deployments are customized as expected")
			for _, bd := range bdList.Items {
				values, _ := loadValues(bd)
				if strings.Contains(bd.Namespace, "cluster-one") {
					Expect(values).To(HaveKeyWithValue("replicas", "1"))
				} else if strings.Contains(bd.Namespace, "cluster-two") {
					Expect(values).To(HaveKeyWithValue("replicas", "2"))
				} else {
					Expect(values).To(HaveKeyWithValue("replicas", "4"))
				}
			}
			By("setting the customization for cluster two as do not deploy")
			clusterBundle := &v1alpha1.Bundle{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, clusterBundle)
			Expect(err).ToNot(HaveOccurred())

			// do not deploy cluster two
			for i, t := range clusterBundle.Spec.Targets {
				if t.ClusterGroup == "two" {
					clusterBundle.Spec.Targets[i].DoNotDeploy = true
				}
			}
			Expect(k8sClient.Update(ctx, clusterBundle)).ToNot(HaveOccurred())

			By("checking that bundle deployment for cluster two is gone")
			bdList = verifyBundlesDeploymentsAreCreated(2, bdLabels, bundleName)
			for _, bd := range bdList.Items {
				Expect(bd.Namespace).ToNot(ContainSubstring("cluster-two"))
				values, _ := loadValues(bd)
				if strings.Contains(bd.Namespace, "cluster-one") {
					Expect(values).To(HaveKeyWithValue("replicas", "1"))
				} else if strings.Contains(bd.Namespace, "cluster-three") {
					Expect(values).To(HaveKeyWithValue("replicas", "4"))
				}
			}
		})
	})
})

func verifyBundlesDeploymentsAreCreated(numBundleDeployments int, bdLabels map[string]string, bundleName string) *v1alpha1.BundleDeploymentList {
	var bdList *v1alpha1.BundleDeploymentList
	bdLabels["fleet.cattle.io/bundle-name"] = bundleName
	Eventually(func() int {
		bdList = &v1alpha1.BundleDeploymentList{}
		err := k8sClient.List(ctx, bdList, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(bdLabels)})
		Expect(err).NotTo(HaveOccurred())

		return len(bdList.Items)
	}).Should(Equal(numBundleDeployments))

	return bdList
}

func waitForBundleToBeReady(bundleName string) {
	Eventually(func() bool {
		bundle := &v1alpha1.Bundle{}
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, bundle)
		Expect(err).NotTo(HaveOccurred())
		for _, condition := range bundle.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				return true
			}
		}
		return false
	}).Should(BeTrue())
}

// creates:
// - three clusters
// - four cluster groups: one per cluster and another that matches all clusters
// - a namespace per cluster. This is to simulate the namespace created for placing the BundleDeployments, this
// is done by another controller, then it is set in the status field.
func createClustersAndClusterGroups() {
	clusterNames := []string{"one", "two", "three"}
	for _, cn := range clusterNames {
		clusterNs, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		clusterNs = clusterNs + "cluster-" + cn
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterNs,
			},
		})).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNs}})).ToNot(HaveOccurred())
		})

		clusterOne, err := utils.CreateCluster(ctx, k8sClient, cn, namespace, map[string]string{"cluster": cn, "env": "test"}, clusterNs)
		Expect(err).NotTo(HaveOccurred())
		Expect(clusterOne).To(Not(BeNil()))

		clusterGroup, err := createClusterGroup(cn, namespace, &metav1.LabelSelector{
			MatchLabels: map[string]string{"cluster": cn},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(clusterGroup).To(Not(BeNil()))
	}

	clusterGroupAll, err := createClusterGroup("all", namespace, &metav1.LabelSelector{
		MatchLabels: map[string]string{"env": "test"},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterGroupAll).To(Not(BeNil()))
}
