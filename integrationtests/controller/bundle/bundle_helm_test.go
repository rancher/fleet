package bundle

import (
	"crypto/rand"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("Bundle with helm options", Ordered, func() {
	BeforeAll(func() {
		var err error
		os.Setenv("EXPERIMENTAL_HELM_OPS", "true")
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		createClustersAndClusterGroups()
	})

	var (
		targets                           []v1alpha1.BundleTarget
		targetRestrictions                []v1alpha1.BundleTarget
		bundleName                        string
		bdLabels                          map[string]string
		expectedNumberOfBundleDeployments int
		helmOptions                       *v1alpha1.BundleHelmOptions
	)

	JustBeforeEach(func() {
		bundle, err := utils.CreateHelmBundle(ctx, k8sClient, bundleName, namespace, targets, targetRestrictions, helmOptions)
		Expect(err).NotTo(HaveOccurred())
		Expect(bundle).To(Not(BeNil()))

		// create secret (if helmOptions != nil)
		err = createHelmSecret(k8sClient, helmOptions, namespace)
		Expect(err).NotTo(HaveOccurred())
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
		// delete secret (if helmOptions != nil)
		err = deleteHelmSecret(k8sClient, helmOptions, namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	When("helm options is NOT nil, and has no values", func() {
		BeforeEach(func() {
			helmOptions = &v1alpha1.BundleHelmOptions{}
			bundleName = "helm-not-nil-and-no-values"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
			// simulate targets. All targets are also added to targetRestrictions, which acts as a white list
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targets))
			copy(targetRestrictions, targets)
		})

		It("creates three BundleDeployments with the expected helm options information", func() {
			var bdList = verifyHelmBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName, helmOptions)
			By("and BundleDeployments don't have helm values")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values).To(BeNil())
			}
		})
	})

	When("helm options is NOT nil, and has values", func() {
		BeforeEach(func() {
			helmOptions = &v1alpha1.BundleHelmOptions{
				SecretName:            "supersecret",
				InsecureSkipTLSverify: true,
			}
			bundleName = "helm-not-nil-and-values"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
			// simulate targets. All targets are also added to targetRestrictions, which acts as a white list
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targets))
			copy(targetRestrictions, targets)
		})

		It("creates three BundleDeployments with the expected helm options information", func() {
			var bdList = verifyHelmBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName, helmOptions)
			By("and BundleDeployments have the expected values")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values).To(BeNil())
			}
		})
	})

	When("helm options is nil", func() {
		BeforeEach(func() {
			helmOptions = nil
			bundleName = "helm-nil"
			bdLabels = map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			expectedNumberOfBundleDeployments = 3
			// simulate targets. All targets are also added to targetRestrictions, which acts as a white list
			targets = []v1alpha1.BundleTarget{
				{
					ClusterGroup: "all",
				},
			}
			targetRestrictions = make([]v1alpha1.BundleTarget, len(targets))
			copy(targetRestrictions, targets)
		})

		It("creates three BundleDeployments with no helm options information", func() {
			var bdList = verifyHelmBundlesDeploymentsAreCreated(expectedNumberOfBundleDeployments, bdLabels, bundleName, helmOptions)
			By("and BundleDeployments don't have helm values")
			for _, bd := range bdList.Items {
				Expect(bd.Spec.Options.Helm.Values).To(BeNil())
			}
		})
	})
})

func verifyHelmBundlesDeploymentsAreCreated(
	numBundleDeployments int,
	bdLabels map[string]string,
	bundleName string,
	helmOptions *v1alpha1.BundleHelmOptions) *v1alpha1.BundleDeploymentList {
	var bdList *v1alpha1.BundleDeploymentList
	bdLabels["fleet.cattle.io/bundle-name"] = bundleName

	Eventually(func(g Gomega) {
		// check bundle exists
		b := &v1alpha1.Bundle{}
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, b)
		g.Expect(err).NotTo(HaveOccurred())

		bdList = &v1alpha1.BundleDeploymentList{}
		err = k8sClient.List(ctx, bdList, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(bdLabels)})
		Expect(err).NotTo(HaveOccurred())

		g.Expect(len(bdList.Items)).To(Equal(numBundleDeployments))
		for _, bd := range bdList.Items {
			// all bds should have the expected helm options
			g.Expect(bd.Spec.HelmChartOptions).To(Equal(helmOptions))

			// if helmOptions.SecretName != "" it should also create
			// a secret in the bundle deployment namespace that contains
			// the same data as in the bundle namespace
			checkBundleDeploymentSecret(k8sClient, helmOptions, bundleName, namespace, bd.Namespace)

			// the bundle deployment should have the expected finalizer
			g.Expect(controllerutil.ContainsFinalizer(&bd, "fleet.cattle.io/bundle-deployment-finalizer")).To(BeTrue())
		}
	}).Should(Succeed())

	return bdList
}

func getRandBytes(size int) ([]byte, error) {
	buf := make([]byte, size)
	// then we can call rand.Read.
	_, err := rand.Read(buf)

	return buf, err
}

func createHelmSecret(c client.Client, helmOptions *v1alpha1.BundleHelmOptions, ns string) error {
	if helmOptions == nil || helmOptions.SecretName == "" {
		return nil
	}
	username, err := getRandBytes(10)
	if err != nil {
		return err
	}

	password, err := getRandBytes(10)
	if err != nil {
		return err
	}

	certs, err := getRandBytes(20)
	if err != nil {
		return err
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helmOptions.SecretName,
			Namespace: ns,
		},
		Data: map[string][]byte{v1.BasicAuthUsernameKey: username, v1.BasicAuthPasswordKey: password, "cacerts": certs},
		Type: v1.SecretTypeBasicAuth,
	}

	return c.Create(ctx, secret)
}

func deleteHelmSecret(c client.Client, helmOptions *v1alpha1.BundleHelmOptions, ns string) error {
	if helmOptions == nil || helmOptions.SecretName == "" {
		return nil
	}
	nsName := types.NamespacedName{Namespace: ns, Name: helmOptions.SecretName}
	secret := &v1.Secret{}
	err := c.Get(ctx, nsName, secret)
	if err != nil {
		return err
	}

	return c.Delete(ctx, secret)
}

func checkBundleDeploymentSecret(c client.Client, helmOptions *v1alpha1.BundleHelmOptions, bundleName, bNamespace, bdNamespace string) {
	if helmOptions == nil || helmOptions.SecretName == "" {
		// nothing to check
		return
	}

	// get the secret for the bundle
	nsName := types.NamespacedName{Namespace: bNamespace, Name: helmOptions.SecretName}
	bundleSecret := &v1.Secret{}
	err := c.Get(ctx, nsName, bundleSecret)
	Expect(err).NotTo(HaveOccurred())

	// get the secret for the bundle deployment
	bdNsName := types.NamespacedName{Namespace: bdNamespace, Name: helmOptions.SecretName}
	bdSecret := &v1.Secret{}
	err = c.Get(ctx, bdNsName, bdSecret)
	Expect(err).NotTo(HaveOccurred())

	// both secrets have the same data
	Expect(bdSecret.Data).To(Equal(bundleSecret.Data))

	// check that the controller reference is set in the bundle deployment secret
	controller := metav1.GetControllerOf(bdSecret)
	Expect(controller).ToNot(BeNil())

	Expect(controller.Name).To(Equal(bundleName))
	Expect(controller.Kind).To(Equal("BundleDeployment"))
	Expect(controller.APIVersion).To(Equal("fleet.cattle.io/v1alpha1"))
}
