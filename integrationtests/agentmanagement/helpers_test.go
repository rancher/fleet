package agentmanagement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// objectExists returns an AsyncAssertion that succeeds when the object can be
// fetched from the API server.
func objectExists(obj client.Object) AsyncAssertion {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	return Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
	})
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

// newConfigMap builds the ConfigMap representation of cfg, as expected by the
// global config's ConfigMap watch.
func newConfigMap(namespace, name string, cfg *config.Config) *corev1.ConfigMap {
	cm, err := config.ToConfigMap(namespace, name, cfg)
	Expect(err).NotTo(HaveOccurred())
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
