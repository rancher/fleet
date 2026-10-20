package singlecluster_test

import (
	"context"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// localClusterSecret is the kubeconfig secret the bootstrap controller
// creates for the local cluster, in the bootstrap namespace.
const localClusterSecret = "local-cluster"

var _ = Describe("Cluster kubeconfig secret namespaces", func() {
	var (
		clusterNS      string
		secretNS       string
		agentNamespace string
		cluster        *fleet.Cluster
	)

	createNamespace := func(prefix string) string {
		GinkgoHelper()
		name := testenv.AddRandomSuffix(prefix, rand.NewSource(time.Now().UnixNano()))
		Expect(clientUpstream.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(clientUpstream.Delete(context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}))).To(Succeed())
		})
		return name
	}

	createCluster := func(secretNamespace, secretName string) {
		GinkgoHelper()
		cluster = &fleet.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: "kubeconfig-ref"},
			Spec: fleet.ClusterSpec{
				KubeConfigSecret:          secretName,
				KubeConfigSecretNamespace: secretNamespace,
				AgentNamespace:            agentNamespace,
			},
		}
		Expect(clientUpstream.Create(context.TODO(), cluster)).To(Succeed())
		DeferCleanup(func() {
			c := &fleet.Cluster{}
			Expect(clientUpstream.Get(context.TODO(), client.ObjectKeyFromObject(cluster), c)).To(Succeed())
			Expect(clientUpstream.Delete(context.TODO(), c)).To(Succeed())
			// The generated cluster namespace outlives its Cluster.
			if c.Status.Namespace != "" {
				Expect(client.IgnoreNotFound(clientUpstream.Delete(context.TODO(),
					&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: c.Status.Namespace}}))).To(Succeed())
			}
		})
	}

	expectNotImported := func(message string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			c := &fleet.Cluster{}
			g.Expect(clientUpstream.Get(context.TODO(), client.ObjectKeyFromObject(cluster), c)).To(Succeed())
			g.Expect(c.Status.Conditions).To(ContainElement(And(
				HaveField("Type", "Imported"),
				HaveField("Status", corev1.ConditionFalse),
				HaveField("Message", ContainSubstring(message)),
			)))
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			err := clientUpstream.Get(context.TODO(), client.ObjectKey{Name: agentNamespace}, &corev1.Namespace{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "agent namespace %s should not exist: %v", agentNamespace, err)
		}, "10s").Should(Succeed())
	}

	BeforeEach(func() {
		clusterNS = createNamespace("kubeconfig-ref")
		agentNamespace = testenv.AddRandomSuffix("kubeconfig-ref-agent", rand.NewSource(time.Now().UnixNano()))
	})

	It("does not use the local cluster's kubeconfig secret from another namespace", func() {
		createCluster(env.Namespace, localClusterSecret)

		expectNotImported("needs label " + fleet.KubeConfigReferenceableFromLabel)
	})

	It("does not deploy an agent onto the management cluster through a labeled secret", func() {
		local := &corev1.Secret{}
		Expect(clientUpstream.Get(context.TODO(), client.ObjectKey{Namespace: env.Namespace, Name: localClusterSecret}, local)).To(Succeed())

		secretNS = createNamespace("kubeconfig-ref-secrets")
		copied := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: secretNS,
				Name:      "management-kubeconfig",
				Labels:    map[string]string{fleet.KubeConfigReferenceableFromLabel: clusterNS},
			},
			Data: local.Data,
		}
		Expect(clientUpstream.Create(context.TODO(), copied)).To(Succeed())
		createCluster(secretNS, copied.Name)

		expectNotImported("targets the management cluster")
	})
})
