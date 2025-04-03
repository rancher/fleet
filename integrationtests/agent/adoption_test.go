package agent_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Adoption", Label("adopt"), func() {
	var (
		namespace string
		env       adoptEnv
	)

	BeforeEach(func() {
		namespace = createNamespace()
		env = adoptEnv{namespace: namespace, env: &specEnv{namespace: namespace}}
	})

	When("a resource of a bundle deployment is removed", Label("remove-resource"), func() {
		It("should report the deleted resource as missing", func() {
			env.createBundleDeployment("remove-resource", false)
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				g.Expect(cm.Data).To(Equal(map[string]string{"key": "value"}))
				g.Expect(cm.Annotations).To(HaveKeyWithValue("meta.helm.sh/release-name", "remove-resource"))
				g.Expect(cm.Annotations).To(HaveKeyWithValue("objectset.rio.cattle.io/id", "default-remove-resource"))
				g.Expect(cm.Annotations).To(HaveKey("meta.helm.sh/release-namespace"))
				g.Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "Helm"))
				g.Expect(cm.Labels).To(HaveKey("objectset.rio.cattle.io/hash"))
			})
			// This is required for the resource to be properly watched, so that
			// when it is deleted, the bundle changes its status.
			time.Sleep(5 * time.Second)
			env.deleteConfigMap("cm1")
			env.assertBundleDeployment("remove-resource", env.bundleDeploymentResourceMissing)
		})
	})

	When("all labels of a resource of a bundle deployment are removed", Label("remove-labels"), func() {
		It("should report that resource as \"not owned by us\"", func() {
			env.createBundleDeployment("remove-metadata", false)
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				g.Expect(cm.Data).To(Equal(map[string]string{"key": "value"}))
				g.Expect(cm.Annotations).To(HaveKeyWithValue("meta.helm.sh/release-name", "remove-metadata"))
				g.Expect(cm.Annotations).To(HaveKeyWithValue("objectset.rio.cattle.io/id", "default-remove-metadata"))
				g.Expect(cm.Annotations).To(HaveKey("meta.helm.sh/release-namespace"))
				g.Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "Helm"))
				g.Expect(cm.Labels).To(HaveKeyWithValue("objectset.rio.cattle.io/hash", "0f3e1d9d146fa8b290c0de403881184751430e59"))
			})
			env.updateConfigMap("cm1", func(cm *corev1.ConfigMap) {
				cm.Labels = map[string]string{}
			})
			env.assertBundleDeployment("remove-metadata", env.bundleDeploymentNotOwnedByUs)
		})
	})

	When("a bundle deployment adopts a \"clean\" resource", Label("clean"), func() {
		// A clean resource is a resource that does not bear labels or annotations indicating that it would belong to any other resource than our bundle deployment.
		It("verifies that the ConfigMap is adopted and its content merged", func() {
			env.createConfigMap(&corev1.ConfigMap{
				Data: map[string]string{"foo": "bar"},
			})
			env.createBundleDeployment("adopt-clean", true)
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				env.assertConfigMapAdopted(g, &cm)
				g.Expect(cm.Data).To(Equal(map[string]string{"foo": "bar", "key": "value"}))
			})
		})
	})

	When("a bundle deployment adopts a resource with wrangler metadata", Label("wrangler-metadata"), func() {
		It("verifies that the ConfigMap is adopted, its content merged and ownership changed", func() {
			const (
				objectSetHashKey   = "objectset.rio.cattle.io/hash"
				objectSetIDKey     = "objectset.rio.cattle.io/id"
				objectSetHashValue = "33ed67317c57ea78702e369c4c025f8df88553cc"
				objectSetIDValue   = "some-assumed-old-id"
			)
			env.createConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{objectSetHashKey: objectSetHashValue},
					Annotations: map[string]string{objectSetIDKey: objectSetIDValue},
				},
				Data: map[string]string{"foo": "bar"},
			})
			env.createBundleDeployment("adopt-wrangler-metadata", true)
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				env.assertConfigMapAdopted(g, &cm)
				g.Expect(cm.Data).To(Equal(map[string]string{"foo": "bar", "key": "value"}))
				g.Expect(cm.Annotations).ToNot(HaveKeyWithValue(objectSetIDKey, objectSetIDValue))
				g.Expect(cm.Labels).ToNot(HaveKeyWithValue(objectSetHashKey, objectSetHashValue))
			})
		})
	})

	When("a bundle deployment adopts a resource with invalid wrangler metadata", Label("wrangler-metadata"), func() {
		It("verifies that the ConfigMap is adopted and its content merged", func() {
			env.createConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"objectset.rio.cattle.io/hash": "234"},
					Labels:      map[string]string{"objectset.rio.cattle.io/id": "foo123"},
				},
				Data: map[string]string{"foo": "bar"},
			})
			env.createBundleDeployment("adopt-invalid-wrangler-metadata", true)
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				env.assertConfigMapAdopted(g, &cm)
				g.Expect(cm.Data).To(Equal(map[string]string{"foo": "bar", "key": "value"}))
				g.Expect(cm.Annotations).ToNot(HaveKeyWithValue("objectset.rio.cattle.io/id", "foo123"))
				g.Expect(cm.Labels).ToNot(HaveKeyWithValue("objectset.rio.cattle.io/hash", "234"))
			})
		})
	})

	When("a bundle deployment adopts a resource with random metadata", Label("random-metadata"), func() {
		It("verifies that the ConfigMap is adopted and its content merged", func() {
			env.createConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"bar": "xzy"},
					Labels:      map[string]string{"foo": "234"},
				},
				Data: map[string]string{"foo": "bar"},
			})
			env.createBundleDeployment("adopt-random-metadata", true)
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				env.assertConfigMapAdopted(g, &cm)
				g.Expect(cm.Data).To(Equal(map[string]string{"foo": "bar", "key": "value"}))
				g.Expect(cm.Annotations).To(HaveKeyWithValue("bar", "xzy"))
				g.Expect(cm.Labels).To(HaveKeyWithValue("foo", "234"))
			})
		})
	})

	When("a bundle adopts a resource that is deployed by another bundle", Label("competing-bundles"), func() {
		It("should complain about not owning the resource", func() {
			env.createBundleDeployment("one", false)
			env.waitForConfigMap("cm1")
			env.createBundleDeployment("two", true)
			env.assertBundleDeployment("one", env.bundleDeploymentNotOwnedByUs)
			env.assertBundleDeployment("two", env.bundleDeploymentReady)
		})
	})
})

type adoptEnv struct {
	namespace string
	env       *specEnv
}

func (e adoptEnv) createBundleDeployment(name string, takeOwnership bool) {
	bundled := v1alpha1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterNS,
		},
		Spec: v1alpha1.BundleDeploymentSpec{
			DeploymentID: "BundleDeploymentConfigMap",
			Options: v1alpha1.BundleDeploymentOptions{
				DefaultNamespace: e.namespace,
				Helm: &v1alpha1.HelmOptions{
					TakeOwnership: takeOwnership,
				},
			},
		},
	}

	err := k8sClient.Create(context.TODO(), &bundled)
	Expect(err).ToNot(HaveOccurred())
	Expect(bundled).To(Not(BeNil()))
	Expect(bundled.Spec.DeploymentID).ToNot(Equal(bundled.Status.AppliedDeploymentID))
	Expect(bundled.Status.Ready).To(BeFalse())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(
			context.TODO(),
			types.NamespacedName{Namespace: clusterNS, Name: name},
			&bundled,
		)).To(Succeed())
		g.Expect(bundled.Status.Ready).To(BeTrue())
	}).Should(Succeed(), "BundleDeployment not ready: status: %+v", bundled.Status)
	Expect(bundled.Spec.DeploymentID).To(Equal(bundled.Status.AppliedDeploymentID))
}

func (e adoptEnv) waitForConfigMap(name string) {
	Eventually(func() error {
		_, err := e.env.getConfigMap(name)
		return err
	}).Should(Succeed())
}

func (e adoptEnv) createConfigMap(cm *corev1.ConfigMap) {
	cm.Name = "cm1"
	cm.Namespace = e.namespace
	Expect(k8sClient.Create(ctx, cm)).To(Succeed())
	e.waitForConfigMap("cm1")
}

// assertConfigMap checks that the ConfigMap exists and that it passes the
// provided validate function.
func (e adoptEnv) assertConfigMap(validate func(Gomega, corev1.ConfigMap)) {
	cm := corev1.ConfigMap{}
	var err error
	Eventually(func(g Gomega) {
		err = k8sClient.Get(
			ctx,
			types.NamespacedName{Namespace: e.namespace, Name: "cm1"},
			&cm,
		)
		g.Expect(err).ToNot(HaveOccurred())
		validate(g, cm)
	}).Should(Succeed(), "assertConfigMap error: %v in %+v", err, cm)
}

// assertBundleDeployment checks that the BundleDeployment exists and that it
// passes the provided validate function.
func (e adoptEnv) assertBundleDeployment(name string, validate func(*v1alpha1.BundleDeployment) error) {
	bd := v1alpha1.BundleDeployment{}
	var err error
	Eventually(func(g Gomega) {
		err = k8sClient.Get(
			ctx,
			types.NamespacedName{Namespace: clusterNS, Name: name},
			&bd,
		)
		g.Expect(err).ToNot(HaveOccurred())
		err = validate(&bd)
		g.Expect(err).ToNot(HaveOccurred())
	}).Should(Succeed(), "assertBundleDeployment: error %v in %+v", err, bd)
}

// configMapAdoptedAndMerged checks that the ConfigMap is adopted. It may
// need to be extended to check for more labels and annotations.
func (e adoptEnv) assertConfigMapAdopted(g Gomega, cm *corev1.ConfigMap) {
	g.Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "Helm"))
	g.Expect(cm.Annotations).To(HaveKey("meta.helm.sh/release-name"))
	g.Expect(cm.Annotations).To(HaveKey("meta.helm.sh/release-namespace"))
}

func (e adoptEnv) updateConfigMap(name string, update func(*corev1.ConfigMap)) {
	cm := &corev1.ConfigMap{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: e.namespace, Name: name}, cm)).To(Succeed())
		update(cm)
		g.Expect(k8sClient.Update(ctx, cm)).To(Succeed())
	}).Should(Succeed())
}

func (e adoptEnv) deleteConfigMap(name string) {
	cm := &corev1.ConfigMap{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: e.namespace, Name: name}, cm)).To(Succeed())
		g.Expect(k8sClient.Delete(ctx, cm)).To(Succeed())
	}).Should(Succeed())
}

func (e adoptEnv) bundleDeploymentResourceMissing(bd *v1alpha1.BundleDeployment) error {
	const msgPrefix = "BundleDeployment resource:"
	for _, condition := range bd.Status.Conditions {
		if condition.Type != v1alpha1.BundleDeploymentConditionReady {
			continue
		}
		if condition.Status != corev1.ConditionFalse {
			return fmt.Errorf("%s Status is not False", msgPrefix)
		}
		if condition.Reason != "Error" {
			return fmt.Errorf("%s Reason is not Error", msgPrefix)
		}
		if !strings.Contains(condition.Message, "missing") {
			return fmt.Errorf("%s Message does not contain 'missing'", msgPrefix)
		}
	}
	return nil
}

func (e adoptEnv) bundleDeploymentNotOwnedByUs(bd *v1alpha1.BundleDeployment) error {
	for _, condition := range bd.Status.Conditions {
		if condition.Type == v1alpha1.BundleDeploymentConditionReady &&
			condition.Status == corev1.ConditionFalse &&
			condition.Reason == "Error" &&
			strings.Contains(condition.Message, "not owned by us") {
			return nil
		}
	}
	return fmt.Errorf("does not match expected condition")
}

func (e adoptEnv) bundleDeploymentReady(bd *v1alpha1.BundleDeployment) error {
	for _, condition := range bd.Status.Conditions {
		if condition.Type == v1alpha1.BundleDeploymentConditionReady &&
			condition.Status == corev1.ConditionTrue {
			return nil
		}
	}
	return fmt.Errorf("does not match expected condition")
}
