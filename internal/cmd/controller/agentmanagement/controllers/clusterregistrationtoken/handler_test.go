package clusterregistrationtoken

import (
	"errors"
	"time"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClusterRegistrationToken OnChange", func() {
	const (
		systemNamespace             = "fleet-system"
		systemRegistrationNamespace = "cattle-fleet-clusters-system"
	)

	var (
		token           *fleet.ClusterRegistrationToken
		status          fleet.ClusterRegistrationTokenStatus
		saCache         *fake.MockCacheInterface[*corev1.ServiceAccount]
		tokenController *fake.MockControllerInterface[*fleet.ClusterRegistrationToken, *fleet.ClusterRegistrationTokenList]
		h               *handler
		notFound        = apierrors.NewNotFound(schema.GroupResource{}, "")
	)

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		saCache = fake.NewMockCacheInterface[*corev1.ServiceAccount](ctrl)
		tokenController = fake.NewMockControllerInterface[*fleet.ClusterRegistrationToken, *fleet.ClusterRegistrationTokenList](ctrl)

		h = &handler{
			systemNamespace:             systemNamespace,
			systemRegistrationNamespace: systemRegistrationNamespace,
			serviceAccountCache:         saCache,
			clusterRegistrationTokens:   tokenController,
		}

		token = &fleet.ClusterRegistrationToken{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "import-token-local",
				Namespace:         "fleet-default",
				CreationTimestamp: metav1.Now(),
			},
			Spec: fleet.ClusterRegistrationTokenSpec{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
		}
		status = fleet.ClusterRegistrationTokenStatus{}
	})

	Describe("no broad secret-read Role is granted", func() {
		BeforeEach(func() {
			saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, notFound)
			// deleteExpired enqueues a re-check when the TTL has not yet elapsed.
			tokenController.EXPECT().EnqueueAfter(gomock.Any(), gomock.Any(), gomock.Any())
		})

		It("does not return a Role that grants access to all secrets in systemRegistrationNamespace", func() {
			objs, _, err := h.OnChange(token, status)
			Expect(err).ToNot(HaveOccurred())

			for _, obj := range objs {
				role, ok := obj.(*rbacv1.Role)
				if !ok || role.Namespace != systemRegistrationNamespace {
					continue
				}
				for _, rule := range role.Rules {
					for _, resource := range rule.Resources {
						if resource == "secrets" {
							Expect(rule.ResourceNames).NotTo(BeEmpty(),
								"found a Role in %s granting access to all secrets",
								systemRegistrationNamespace)
						}
					}
				}
			}
		})

		It("returns exactly 3 objects: ServiceAccount, Role for clusterregistrations, and RoleBinding", func() {
			objs, _, err := h.OnChange(token, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(objs).To(HaveLen(3))

			kinds := map[string]int{}
			for _, obj := range objs {
				switch obj.(type) {
				case *corev1.ServiceAccount:
					kinds["ServiceAccount"]++
				case *rbacv1.Role:
					kinds["Role"]++
				case *rbacv1.RoleBinding:
					kinds["RoleBinding"]++
				}
			}
			Expect(kinds["ServiceAccount"]).To(Equal(1))
			Expect(kinds["Role"]).To(Equal(1))
			Expect(kinds["RoleBinding"]).To(Equal(1))
		})

		It("does not create any object in systemRegistrationNamespace", func() {
			objs, _, err := h.OnChange(token, status)
			Expect(err).ToNot(HaveOccurred())

			for _, obj := range objs {
				if m, ok := obj.(metav1.Object); ok {
					Expect(m.GetNamespace()).NotTo(Equal(systemRegistrationNamespace),
						"handler must not create objects in %s; scoped RBAC is the clusterregistration controller's responsibility",
						systemRegistrationNamespace)
				}
			}
		})
	})

	Describe("tokens with no TTL are rejected when enforceTTL is enabled", func() {
		BeforeEach(func() {
			h.enforceTTL = true
			token.Spec.TTL = nil
		})

		It("deletes the token and returns no error (no retry)", func() {
			tokenController.EXPECT().Delete(token.Namespace, token.Name, nil).Return(nil)

			objs, _, err := h.OnChange(token, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(objs).To(BeNil())
		})

		It("returns no error even when deletion fails with NotFound (already gone)", func() {
			tokenController.EXPECT().Delete(token.Namespace, token.Name, nil).Return(notFound)

			_, _, err := h.OnChange(token, status)
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns a deletion error when the API call fails", func() {
			tokenController.EXPECT().Delete(token.Namespace, token.Name, nil).Return(errors.New("server unavailable"))

			_, _, err := h.OnChange(token, status)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("deleting ClusterRegistrationToken"))
		})

		It("also rejects a token whose TTL duration is zero, without returning an error", func() {
			token.Spec.TTL = &metav1.Duration{Duration: 0}
			tokenController.EXPECT().Delete(token.Namespace, token.Name, nil).Return(nil)

			_, _, err := h.OnChange(token, status)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("tokens with no TTL are accepted when enforceTTL is disabled", func() {
		BeforeEach(func() {
			h.enforceTTL = false
			token.Spec.TTL = nil
		})

		It("does not delete or reject the token", func() {
			saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, notFound)

			objs, _, err := h.OnChange(token, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(objs).To(HaveLen(3))
		})
	})
})
