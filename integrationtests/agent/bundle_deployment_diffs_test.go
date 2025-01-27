package agent_test

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BundleDeployment diff", func() {
	const (
		svcName    = "svc-test"
		cfgMapName = "cm-test"
	)

	var (
		namespace string
		name      string
		deplID    string
		env       *specEnv
		patches   []v1alpha1.ComparePatch
	)

	createBundleDeployment := func(name string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: deplID,
				Options: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: namespace,
					Diff: &v1alpha1.DiffOptions{
						ComparePatches: patches,
					},
				},
			},
		}

		err := k8sClient.Create(context.TODO(), &bundled)
		Expect(err).To(BeNil())
		Expect(bundled).To(Not(BeNil()))
	}

	createNamespace := func() string {
		newNs, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: newNs}}
		Expect(k8sClient.Create(context.Background(), ns)).ToNot(HaveOccurred())

		return newNs
	}

	When("A bundle deployment is created with a bundle diff", func() {
		JustBeforeEach(func() {
			namespace = createNamespace()
			deplID = "v1"
			patches = []v1alpha1.ComparePatch{
				{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       cfgMapName,
					Namespace:  namespace,
					Operations: []v1alpha1.Operation{
						{
							Op:   "remove",
							Path: "/data",
						},
					},
				},
				{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       svcName,
					Namespace:  namespace,
					Operations: []v1alpha1.Operation{
						{
							Op:   "remove",
							Path: "/spec/ports",
						},
					},
				},
			}

			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
			Eventually(func(g Gomega) {
				g.Expect(env.isBundleDeploymentReadyAndNotModified(name)).To(BeTrue())
				_, err := env.getService(svcName)
				g.Expect(err).NotTo(HaveOccurred())
			}).Should(Succeed())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(
					context.TODO(),
					&v1alpha1.BundleDeployment{ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name}},
				)).ToNot(HaveOccurred())
			})
		})

		Context("Modifying values covered by the diff", func() {
			BeforeEach(func() {
				name = "diff-update-test"
			})

			It("Keeps the bundle deployment in ready state", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Ports[0].Port = 4242
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				Consistently(func(g Gomega) {
					bd := &v1alpha1.BundleDeployment{}
					err := k8sClient.Get(
						context.TODO(),
						types.NamespacedName{Namespace: clusterNS, Name: name},
						bd,
						&client.GetOptions{},
					)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(bd.Status.NonModified).To(BeTrue())
				}, 5*time.Second, time.Second).Should(Succeed())
			})
		})

		Context("Deleting values covered by the diff", func() {
			BeforeEach(func() {
				name = "diff-delete-test"
			})

			It("Keeps the bundle deployment in ready state", func() {
				cm, err := env.getConfigMap(cfgMapName)
				Expect(err).ToNot(HaveOccurred())

				patchedCM := cm.DeepCopy()
				patchedCM.Data = nil
				Expect(k8sClient.Patch(ctx, patchedCM, client.StrategicMergeFrom(&cm))).NotTo(HaveOccurred())

				Consistently(func(g Gomega) {
					bd := &v1alpha1.BundleDeployment{}
					err := k8sClient.Get(
						context.TODO(),
						types.NamespacedName{Namespace: clusterNS, Name: name},
						bd,
						&client.GetOptions{},
					)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(bd.Status.NonModified).To(BeTrue())
				}, 5*time.Second, time.Second).Should(Succeed())
			})
		})

		Context("Modifying values not covered by the diff", func() {
			BeforeEach(func() {
				name = "diff-modif-uncovered-test"
			})

			It("Updates the bundle deployment status to modified", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Selector = map[string]string{"env": "modification"}
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       svcName,
						Create:     false,
						Delete:     false,
						Patch:      `{"spec":{"selector":{"app.kubernetes.io/name":"MyApp"}}}`,
					}
					env.isNotReadyAndModified(
						g,
						name,
						modifiedStatus,
						fmt.Sprintf(
							`service.v1 %s/%s modified {"spec":{"selector":{"app.kubernetes.io/name":"MyApp"}}}`,
							namespace,
							svcName,
						),
					)
				}).Should(Succeed())
			})
		})
	})
})
