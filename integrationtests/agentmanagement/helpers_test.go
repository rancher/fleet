package agentmanagement_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/version"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func newNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

// clusterRole returns a ClusterRole with the given name (cluster-scoped).
func clusterRole(name string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

// objectExists returns an AsyncAssertion that succeeds when the object can be
// fetched from the API server.
func objectExists(obj client.Object) AsyncAssertion {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	return Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
	})
}

// namespaceExists returns an AsyncAssertion that succeeds when a namespace with
// the given name exists.
func namespaceExists(name string) AsyncAssertion {
	return objectExists(newNamespace(name))
}

// namespaceIsGone returns an AsyncAssertion that succeeds when no namespace with
// the given name exists.
func namespaceIsGone(name string) AsyncAssertion {
	return objectGone(newNamespace(name))
}

// objectGone returns an AsyncAssertion that succeeds once obj can no longer be
// fetched from the API server.
func objectGone(obj client.Object) AsyncAssertion {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	return Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, key, obj)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected %s to be gone, got err: %v", key, err)
	})
}

// objectAbsent returns an assertion body, for use with Consistently, that
// holds while obj cannot be fetched. Unlike a bare NotTo(Succeed()) it accepts
// only NotFound, so a connection or authorization error fails the spec instead
// of reading as "the controller never created it".
func objectAbsent(obj client.Object) func(Gomega) {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	return func(g Gomega) {
		err := k8sClient.Get(ctx, key, obj)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected %s to be absent, got err: %v", key, err)
	}
}

// deleteNamespace removes a namespace for good. envtest runs no namespace
// controller, so a deleted namespace stays Terminating forever unless the
// kubernetes finalizer is cleared through the finalize subresource.
func deleteNamespace(name string) {
	GinkgoHelper()

	ns := newNamespace(name)
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).To(Succeed())

	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, ns)
		if apierrors.IsNotFound(err) {
			return
		}
		g.Expect(err).NotTo(HaveOccurred())

		ns.Spec.Finalizers = nil
		g.Expect(client.IgnoreNotFound(k8sClient.SubResource("finalize").Update(ctx, ns))).To(Succeed())
	}).Should(Succeed())

	namespaceIsGone(name).Should(Succeed())
}

// newConfigMap builds the ConfigMap representation of cfg, as expected by the
// global config's ConfigMap watch.
func newConfigMap(namespace, name string, cfg *config.Config) *corev1.ConfigMap {
	cm, err := config.ToConfigMap(namespace, name, cfg)
	Expect(err).NotTo(HaveOccurred())
	cm.Annotations = map[string]string{config.VersionAnnotation: version.Version}
	return cm
}

// newGeneratedNamespace returns a namespace object whose actual name is
// assigned by the API server on creation, using prefix as the GenerateName.
func newGeneratedNamespace(prefix string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: prefix},
	}
}

// newCluster returns a minimal Cluster in the given namespace.
func newCluster(namespace, name string) *fleet.Cluster {
	return &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
}

// clusterNamespaceName predicts the generated cluster namespace name for a
// Cluster identified by its (namespace, name), following the documented
// format of Cluster.Status.Namespace: "cluster-<namespace>-<name>-<hash>".
func clusterNamespaceName(namespace, name string) string {
	return names.SafeConcatName("cluster", namespace, name,
		names.KeyHash(namespace+"::"+name))
}

// newBundleDeployment returns a minimal BundleDeployment in the given namespace.
func newBundleDeployment(namespace, name string) *fleet.BundleDeployment {
	return &fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
}

// newClusterRegistrationToken returns a minimal ClusterRegistrationToken with
// the given TTL (nil for no TTL).
func newClusterRegistrationToken(namespace, name string, ttl *metav1.Duration) *fleet.ClusterRegistrationToken {
	return &fleet.ClusterRegistrationToken{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       fleet.ClusterRegistrationTokenSpec{TTL: ttl},
	}
}

// newClusterRegistration returns a ClusterRegistration identifying itself with
// the given client ID, client random, and optional registration token.
func newClusterRegistration(namespace, name, clientID, clientRandom, tokenName string) *fleet.ClusterRegistration {
	cr := &fleet.ClusterRegistration{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: fleet.ClusterRegistrationSpec{
			ClientID:     clientID,
			ClientRandom: clientRandom,
		},
	}
	if tokenName != "" {
		cr.Labels = map[string]string{fleet.RegistrationTokenLabel: tokenName}
	}
	return cr
}

// populateServiceAccountTokenSecret waits for the "<saName>-token" Secret to
// appear in namespace and fills in tokenValue, standing in for the token
// controller that populates it in a real cluster (envtest runs no
// controller-manager to do this on its own). Handlers that ask for a service
// account token block until the Secret carries one.
func populateServiceAccountTokenSecret(namespace, saName, tokenValue string) {
	GinkgoHelper()

	key := types.NamespacedName{Namespace: namespace, Name: saName + "-token"}
	// Get and update must share the retry, or a resourceVersion conflict
	// between them fails the spec instead of being retried. Only the token
	// key is set, so the rest of a service account token Secret's data
	// (ca.crt, namespace) survives if anything ever populates it.
	Eventually(func(g Gomega) {
		secret := &corev1.Secret{}
		g.Expect(k8sClient.Get(ctx, key, secret)).To(Succeed())
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[corev1.ServiceAccountTokenKey] = []byte(tokenValue)
		g.Expect(k8sClient.Update(ctx, secret)).To(Succeed())
	}).Should(Succeed())
}

// agentBundleName returns the name of the agent bundle Fleet maintains for a
// cluster: the well-known agent name joined with the cluster name.
func agentBundleName(clusterName string) string {
	return names.SafeConcatName("fleet-agent", clusterName)
}

// agentBundle fetches the agent bundle of a cluster, failing the spec if it
// does not exist (yet).
func agentBundle(namespace, clusterName string) *fleet.Bundle {
	GinkgoHelper()

	bundle := &fleet.Bundle{}
	key := types.NamespacedName{Namespace: namespace, Name: agentBundleName(clusterName)}
	Expect(k8sClient.Get(ctx, key, bundle)).To(Succeed())
	return bundle
}

// agentBundleExists returns an AsyncAssertion that succeeds once the agent
// bundle of a cluster exists.
func agentBundleExists(namespace, clusterName string) AsyncAssertion {
	return objectExists(&fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: agentBundleName(clusterName)},
	})
}

// agentManifestObjects decodes the multi-document manifest the agent bundle
// carries as its single "agent.yaml" resource.
func agentManifestObjects(bundle *fleet.Bundle) []*unstructured.Unstructured {
	GinkgoHelper()

	Expect(bundle.Spec.Resources).To(HaveLen(1))
	Expect(bundle.Spec.Resources[0].Name).To(Equal("agent.yaml"))

	var objs []*unstructured.Unstructured
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(bundle.Spec.Resources[0].Content)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		Expect(err).NotTo(HaveOccurred())
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		obj := &unstructured.Unstructured{}
		Expect(yaml.Unmarshal(doc, &obj.Object)).To(Succeed())
		objs = append(objs, obj)
	}
	return objs
}

// agentManifestKinds lists "<kind>/<namespace>/<name>" for every object in the
// agent manifest, so a spec can assert on the shape of the whole manifest.
func agentManifestKinds(bundle *fleet.Bundle) []string {
	GinkgoHelper()

	var out []string
	for _, obj := range agentManifestObjects(bundle) {
		out = append(out, strings.Join([]string{obj.GetKind(), obj.GetNamespace(), obj.GetName()}, "/"))
	}
	return out
}

// agentDeployment returns the agent Deployment carried by the agent bundle.
func agentDeployment(bundle *fleet.Bundle) *appsv1.Deployment {
	GinkgoHelper()

	for _, obj := range agentManifestObjects(bundle) {
		if obj.GetKind() != "Deployment" {
			continue
		}
		deployment := &appsv1.Deployment{}
		Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, deployment)).To(Succeed())
		return deployment
	}

	Fail("the agent manifest carries no Deployment")
	return nil
}

// agentContainer returns the agent container of the Deployment carried by the
// agent bundle.
func agentContainer(bundle *fleet.Bundle) corev1.Container {
	GinkgoHelper()

	containers := agentDeployment(bundle).Spec.Template.Spec.Containers
	Expect(containers).To(HaveLen(1))
	return containers[0]
}

// touchNamespace applies a label to a namespace, standing in for any change a
// cluster administrator may make to it.
func touchNamespace(name, value string) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		ns := &corev1.Namespace{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, ns)).To(Succeed())
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels["fleet-integration-tests/touched"] = value
		g.Expect(k8sClient.Update(ctx, ns)).To(Succeed())
	}).Should(Succeed())
}

// updateCluster applies mutate to a cluster, retrying on conflict with the
// controllers that write the same object.
func updateCluster(namespace, name string, mutate func(*fleet.Cluster)) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		cluster := &fleet.Cluster{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster)).To(Succeed())
		mutate(cluster)
		g.Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
	}).Should(Succeed())
}

// getCluster fetches a cluster inside an Eventually or Consistently body.
func getCluster(g Gomega, namespace, name string) *fleet.Cluster {
	cluster := &fleet.Cluster{}
	g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster)).To(Succeed())
	return cluster
}

// setConfig replaces the global configuration for the duration of the current
// spec and restores it afterwards. mutate works on a shallow copy, so it must
// replace maps and slices rather than change them in place, or the change
// leaks into the configuration that is restored.
func setConfig(mutate func(*config.Config)) {
	GinkgoHelper()

	previous := config.Get()
	restore := *previous
	DeferCleanup(func() {
		config.Set(&restore)
	})

	updated := *previous
	mutate(&updated)
	config.Set(&updated)
}
