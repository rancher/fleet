package singlecluster_test

// Tests that agent-initiated ClusterRegistration binds a Cluster's ClientID to
// the registration token used at its first creation. A later registration for
// the same ClientID, presenting a different token, is not granted.

import (
	"context"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/e2e/testenv"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("ClusterRegistration token binding", Label("cluster-registration"), func() {
	var (
		ctx = context.Background()
		r   = rand.New(rand.NewSource(GinkgoRandomSeed()))

		clientID          string
		registeredToken   *fleet.ClusterRegistrationToken
		otherToken        *fleet.ClusterRegistrationToken
		registeredRequest *fleet.ClusterRegistration
		clusterName       string
	)

	createToken := func(name string) *fleet.ClusterRegistrationToken {
		token := &fleet.ClusterRegistrationToken{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: env.Namespace,
			},
		}
		Expect(clientUpstream.Create(ctx, token)).To(Succeed())

		Eventually(func(g Gomega) string {
			latest := &fleet.ClusterRegistrationToken{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(token), latest)).To(Succeed())
			return latest.Status.SecretName
		}).ShouldNot(BeEmpty(), "token secret should be issued")

		return token
	}

	BeforeEach(func() {
		clientID = testenv.NewNamespaceName("e2e-clientid", r)

		registeredToken = createToken(testenv.NewNamespaceName("registered-token", r))
		otherToken = createToken(testenv.NewNamespaceName("other-token", r))

		registeredRequest = &fleet.ClusterRegistration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "registered-request-",
				Namespace:    env.Namespace,
				Labels: map[string]string{
					fleet.RegistrationTokenLabel: registeredToken.Name,
				},
			},
			Spec: fleet.ClusterRegistrationSpec{
				ClientID:     clientID,
				ClientRandom: "registered-random",
			},
		}
		Expect(clientUpstream.Create(ctx, registeredRequest)).To(Succeed())

		Eventually(func(g Gomega) bool {
			latest := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(registeredRequest), latest)).To(Succeed())
			registeredRequest = latest
			return latest.Status.Granted
		}).Should(BeTrue(), "first registration should be granted")

		clusterName = registeredRequest.Status.ClusterName
	})

	AfterEach(func() {
		_ = clientUpstream.Delete(ctx, registeredRequest)
		_ = clientUpstream.Delete(ctx, registeredToken)
		_ = clientUpstream.Delete(ctx, otherToken)
		if clusterName != "" {
			_ = clientUpstream.Delete(ctx, &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: env.Namespace},
			})
		}
	})

	It("does not grant a later registration for the same ClientID presenting a different token", func() {
		cluster := &fleet.Cluster{}
		Expect(clientUpstream.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: clusterName}, cluster)).To(Succeed())
		Expect(cluster.Annotations[fleet.RegisteredTokenAnnotation]).To(Equal(registeredToken.Name))

		differentTokenRequest := &fleet.ClusterRegistration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "different-token-request-",
				Namespace:    env.Namespace,
				Labels: map[string]string{
					fleet.RegistrationTokenLabel: otherToken.Name,
				},
			},
			Spec: fleet.ClusterRegistrationSpec{
				ClientID:     clientID,
				ClientRandom: "different-token-random",
			},
		}
		Expect(clientUpstream.Create(ctx, differentTokenRequest)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(ctx, differentTokenRequest)
		})

		// The request must not be granted.
		Consistently(func(g Gomega) fleet.ClusterRegistrationStatus {
			latest := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(differentTokenRequest), latest)).To(Succeed())
			return latest.Status
		}).Should(Equal(fleet.ClusterRegistrationStatus{}))

		// The first registration stays granted.
		latestRegistered := &fleet.ClusterRegistration{}
		Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(registeredRequest), latestRegistered)).To(Succeed())
		Expect(latestRegistered.Status.Granted).To(BeTrue())
		Expect(latestRegistered.Status.ClusterName).To(Equal(clusterName))

		// The Cluster still records the first token, and no service account
		// was created for the request in its namespace.
		latestCluster := &fleet.Cluster{}
		Expect(clientUpstream.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: clusterName}, latestCluster)).To(Succeed())
		Expect(latestCluster.Annotations[fleet.RegisteredTokenAnnotation]).To(Equal(registeredToken.Name))

		saList := &corev1.ServiceAccountList{}
		Expect(clientUpstream.List(ctx, saList, client.InNamespace(latestCluster.Status.Namespace))).To(Succeed())
		for _, sa := range saList.Items {
			Expect(sa.Annotations[fleet.ClusterRegistrationAnnotation]).ToNot(Equal(differentTokenRequest.Name))
		}
	})

	It("grants a registration with a different token once the Cluster records that token", func() {
		newToken := createToken(testenv.NewNamespaceName("new-token", r))
		DeferCleanup(func() {
			_ = clientUpstream.Delete(ctx, newToken)
		})

		newRequest := &fleet.ClusterRegistration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "new-request-",
				Namespace:    env.Namespace,
				Labels: map[string]string{
					fleet.RegistrationTokenLabel: newToken.Name,
				},
			},
			Spec: fleet.ClusterRegistrationSpec{
				ClientID:     clientID,
				ClientRandom: "new-random",
			},
		}
		Expect(clientUpstream.Create(ctx, newRequest)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(ctx, newRequest)
		})

		Consistently(func(g Gomega) bool {
			latest := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(newRequest), latest)).To(Succeed())
			return latest.Status.Granted
		}).Should(BeFalse())

		cluster := &fleet.Cluster{}
		Expect(clientUpstream.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: clusterName}, cluster)).To(Succeed())
		patch := client.MergeFrom(cluster.DeepCopy())
		cluster.Annotations[fleet.RegisteredTokenAnnotation] = newToken.Name
		Expect(clientUpstream.Patch(ctx, cluster, patch)).To(Succeed())

		Eventually(func(g Gomega) {
			latest := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(newRequest), latest)).To(Succeed())
			g.Expect(latest.Status.Granted).To(BeTrue())
			g.Expect(latest.Status.ClusterName).To(Equal(clusterName))
		}).Should(Succeed())
	})

	It("binds a Cluster without recorded token to its granted registration and reports a mismatch", func() {
		cluster := &fleet.Cluster{}
		Expect(clientUpstream.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: clusterName}, cluster)).To(Succeed())
		patch := client.MergeFrom(cluster.DeepCopy())
		delete(cluster.Annotations, fleet.RegisteredTokenAnnotation)
		Expect(clientUpstream.Patch(ctx, cluster, patch)).To(Succeed())

		otherRequest := &fleet.ClusterRegistration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "other-request-",
				Namespace:    env.Namespace,
				Labels: map[string]string{
					fleet.RegistrationTokenLabel: otherToken.Name,
				},
			},
			Spec: fleet.ClusterRegistrationSpec{
				ClientID:     clientID,
				ClientRandom: "other-random",
			},
		}
		Expect(clientUpstream.Create(ctx, otherRequest)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(ctx, otherRequest)
		})

		Eventually(func(g Gomega) {
			events := &corev1.EventList{}
			g.Expect(clientUpstream.List(ctx, events, client.InNamespace(env.Namespace))).To(Succeed())
			g.Expect(events.Items).To(ContainElement(And(
				HaveField("InvolvedObject.Name", clusterName),
				HaveField("Reason", "RegistrationTokenMismatch"),
				HaveField("Type", corev1.EventTypeWarning),
			)))
		}).Should(Succeed())

		latest := &fleet.ClusterRegistration{}
		Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(otherRequest), latest)).To(Succeed())
		Expect(latest.Status.Granted).To(BeFalse())
	})

	It("does not grant a registration whose token no longer exists and keeps the granted one", func() {
		Expect(clientUpstream.Delete(ctx, registeredToken)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(clientUpstream.Get(ctx, client.ObjectKeyFromObject(registeredToken), &fleet.ClusterRegistrationToken{}))
		}).Should(BeTrue())

		sameTokenRequest := &fleet.ClusterRegistration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "same-token-request-",
				Namespace:    env.Namespace,
				Labels: map[string]string{
					fleet.RegistrationTokenLabel: registeredToken.Name,
				},
			},
			Spec: fleet.ClusterRegistrationSpec{
				ClientID:     clientID,
				ClientRandom: "same-token-random",
			},
		}
		Expect(clientUpstream.Create(ctx, sameTokenRequest)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(ctx, sameTokenRequest)
		})

		Consistently(func(g Gomega) {
			latest := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(sameTokenRequest), latest)).To(Succeed())
			g.Expect(latest.Status.Granted).To(BeFalse())

			first := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(registeredRequest), first)).To(Succeed())
			g.Expect(first.Status.Granted).To(BeTrue())
		}).Should(Succeed())
	})

	It("does not grant a registration for a cluster with kubeConfigSecret to another token than its import token", func() {
		importedClientID := testenv.NewNamespaceName("e2e-imported-clientid", r)
		imported := &fleet.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testenv.NewNamespaceName("imported", r),
				Namespace: env.Namespace,
			},
			Spec: fleet.ClusterSpec{
				ClientID:         importedClientID,
				KubeConfigSecret: "missing-kubeconfig",
			},
		}
		Expect(clientUpstream.Create(ctx, imported)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(ctx, imported)
		})

		Eventually(func(g Gomega) string {
			latest := &fleet.Cluster{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(imported), latest)).To(Succeed())
			return latest.Status.Namespace
		}).ShouldNot(BeEmpty(), "cluster namespace should be assigned")

		otherTokenRequest := &fleet.ClusterRegistration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "other-token-request-",
				Namespace:    env.Namespace,
				Labels: map[string]string{
					fleet.RegistrationTokenLabel: otherToken.Name,
				},
			},
			Spec: fleet.ClusterRegistrationSpec{
				ClientID:     importedClientID,
				ClientRandom: "other-token-random",
			},
		}
		Expect(clientUpstream.Create(ctx, otherTokenRequest)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(ctx, otherTokenRequest)
		})

		Eventually(func(g Gomega) string {
			latest := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(otherTokenRequest), latest)).To(Succeed())
			return latest.Status.ClusterName
		}).Should(Equal(imported.Name))

		Consistently(func(g Gomega) bool {
			latest := &fleet.ClusterRegistration{}
			g.Expect(clientUpstream.Get(ctx, client.ObjectKeyFromObject(otherTokenRequest), latest)).To(Succeed())
			return latest.Status.Granted
		}).Should(BeFalse())
	})
})
