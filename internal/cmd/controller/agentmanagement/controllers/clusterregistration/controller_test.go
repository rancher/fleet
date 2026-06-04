package clusterregistration

import (
	"errors"

	"github.com/rancher/wrangler/v3/pkg/generic"
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClusterRegistration OnChange", func() {
	var (
		request *fleet.ClusterRegistration
		status  fleet.ClusterRegistrationStatus
		cluster *fleet.Cluster
		sa      *corev1.ServiceAccount

		saCache                       *fake.MockCacheInterface[*corev1.ServiceAccount]
		secretCache                   *fake.MockCacheInterface[*corev1.Secret]
		secretController              *fake.MockControllerInterface[*corev1.Secret, *corev1.SecretList]
		clusterClient                 *fake.MockClientInterface[*fleet.Cluster, *fleet.ClusterList]
		clusterRegistrationController *fake.MockControllerInterface[*fleet.ClusterRegistration, *fleet.ClusterRegistrationList]
		clusterCache                  *fake.MockCacheInterface[*fleet.Cluster]
		tokenCache                    *fake.MockCacheInterface[*fleet.ClusterRegistrationToken]
		h                             *handler
		notFound                      = apierrors.NewNotFound(schema.GroupResource{}, "")
		anError                       = errors.New("an error occurred")
	)

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		saCache = fake.NewMockCacheInterface[*corev1.ServiceAccount](ctrl)
		secretCache = fake.NewMockCacheInterface[*corev1.Secret](ctrl)
		secretController = fake.NewMockControllerInterface[*corev1.Secret, *corev1.SecretList](ctrl)
		clusterClient = fake.NewMockClientInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
		clusterRegistrationController = fake.NewMockControllerInterface[*fleet.ClusterRegistration, *fleet.ClusterRegistrationList](ctrl)
		clusterCache = fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
		tokenCache = fake.NewMockCacheInterface[*fleet.ClusterRegistrationToken](ctrl)

		h = &handler{
			systemNamespace:             "fleet-system",
			systemRegistrationNamespace: "fleet-clusters-system",
			clusterRegistration:         clusterRegistrationController,
			clusterCache:                clusterCache,
			clusters:                    clusterClient,
			secretsCache:                secretCache,
			secrets:                     secretController,
			serviceAccountCache:         saCache,
			tokenCache:                  tokenCache,
		}

	})

	Context("ClusterRegistration already granted", func() {
		BeforeEach(func() {
			status = fleet.ClusterRegistrationStatus{
				Granted: true,
			}
		})

		It("does nothing", func() {
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).To(Equal(generic.ErrSkip))
			Expect(objs).To(BeEmpty())
			Expect(newStatus.Granted).To(BeTrue())
		})
	})

	Context("Cluster is missing", func() {
		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				Spec: fleet.ClusterRegistrationSpec{
					ClientID: "client-id",
				},
			}
			status = fleet.ClusterRegistrationStatus{}

			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return(nil, nil)
			// code panics if cache.Get returns an error or nil
			clusterCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).Return(nil, notFound)
		})

		When("cluster creation works", func() {
			BeforeEach(func() {
				clusterClient.EXPECT().Create(gomock.Any()).Return(nil, nil).Do(func(obj any) {
					switch cluster := obj.(type) {
					case *fleet.Cluster:
						Expect(cluster.Spec.ClientID).To(Equal("client-id"))
					default:
						Fail("unexpected type")
					}
				})
			})

			It("creates the missing cluster", func() {
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).ToNot(HaveOccurred())
				Expect(objs).To(BeEmpty())
				Expect(newStatus.Granted).To(BeFalse())
			})
		})

		When("cluster creation fails", func() {
			BeforeEach(func() {
				clusterClient.EXPECT().Create(gomock.Any()).Return(nil, anError)
			})

			It("returns an error", func() {
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).To(HaveOccurred())
				Expect(objs).To(BeEmpty())
				Expect(newStatus.Granted).To(BeFalse())
			})
		})
	})

	Context("Cluster exists", func() {
		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "request-1",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID: "client-id",
				},
			}

			cluster = &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					ClientID: "client-id",
				},
			}
			status = fleet.ClusterRegistrationStatus{}

			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return(nil, nil)
			clusterCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).Return(cluster, nil)
		})

		When("cluster status has no namespace", func() {
			It("sets the cluster name into the registrations status", func() {
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).ToNot(HaveOccurred())
				Expect(objs).To(BeEmpty())
				Expect(newStatus.Granted).To(BeFalse())
				Expect(newStatus.ClusterName).To(Equal("cluster"))
			})
		})

		When("service account does not exist", func() {
			BeforeEach(func() {
				cluster.Status = fleet.ClusterStatus{Namespace: "fleet-default"}
				saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, notFound)
				clusterRegistrationController.EXPECT().Update(gomock.Any()).Return(&fleet.ClusterRegistration{}, nil)
			})

			It("creates a new service account", func() {
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).ToNot(HaveOccurred())
				Expect(objs).To(HaveLen(1))
				Expect(newStatus.Granted).To(BeFalse())
				Expect(newStatus.ClusterName).To(Equal("cluster"))
			})
		})

		When("service account secret is missing", func() {
			BeforeEach(func() {
				cluster.Status = fleet.ClusterStatus{Namespace: "fleet-default"}
				// post k8s 1.24 service account without sa.Secrets list
				sa = &corev1.ServiceAccount{}
				saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(sa, nil)
				clusterRegistrationController.EXPECT().Update(gomock.Any()).Return(&fleet.ClusterRegistration{}, nil)
			})

			Context("cannot create secret", func() {
				BeforeEach(func() {
					secretController.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, notFound)
					secretController.EXPECT().Create(gomock.Any()).Return(nil, anError)
				})

				It("creates a new service account and errors", func() {
					objs, _, err := h.OnChange(request, status)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("failed to authorize cluster"))
					Expect(objs).To(BeEmpty())
				})
			})

			Context("authorizeCluster returns nil,nil", func() {
				BeforeEach(func() {
					// pre k8s 1.24 service account has sa.Secrets list
					sa.Secrets = []corev1.ObjectReference{{Name: "tokensecret"}}
					secretCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, notFound)
					secretController.EXPECT().Get(gomock.Any(), "tokensecret", gomock.Any()).Return(nil, nil)
				})

				It("returns early", func() {
					objs, newStatus, err := h.OnChange(request, status)
					Expect(err).ToNot(HaveOccurred())
					Expect(objs).To(BeEmpty())
					Expect(newStatus.ClusterName).To(Equal("cluster"))
					Expect(newStatus.Granted).To(BeFalse())
				})
			})
		})

		When("service account secret exists", func() {
			BeforeEach(func() {
				cluster.Status = fleet.ClusterStatus{Namespace: "fleet-default"}

				sa = &corev1.ServiceAccount{}
				saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(sa, nil)

				// needs token here, otherwise controller will sleep to wait for it
				secret := &corev1.Secret{
					Data: map[string][]byte{"token": []byte("secrettoken")},
				}
				secretController.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(secret, nil)

				clusterRegistrationController.EXPECT().List(gomock.Any(), gomock.Any()).Return(&fleet.ClusterRegistrationList{}, nil)

				clusterRegistrationController.EXPECT().Update(gomock.Any()).Return(&fleet.ClusterRegistration{}, nil)
			})

			Context("agent-initiated flow: no import token found, RegistrationTokenLabel is empty", func() {
				BeforeEach(func() {
					tokenCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, notFound)
				})

				It("grants registration and creates 6 objects without credential RBAC", func() {
					objs, newStatus, err := h.OnChange(request, status)
					Expect(err).ToNot(HaveOccurred())
					Expect(objs).To(HaveLen(6))
					Expect(newStatus.Granted).To(BeTrue())
				})
			})

			Context("manager-initiated flow: import token exists", func() {
				BeforeEach(func() {
					importToken := &fleet.ClusterRegistrationToken{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "import-token-cluster",
							Namespace: "fleet-default",
							UID:       types.UID("test-token-uid"),
						},
					}
					tokenCache.EXPECT().Get(gomock.Any(), "import-token-cluster").Return(importToken, nil)
				})

				It("grants registration and creates 8 objects including scoped credential RBAC", func() {
					objs, newStatus, err := h.OnChange(request, status)
					Expect(err).ToNot(HaveOccurred())
					Expect(objs).To(HaveLen(8))
					Expect(newStatus.Granted).To(BeTrue())

					// Locate the Role created in systemRegistrationNamespace
					// to verify access is scoped to a single secret, not wildcard.
					var credRole *rbacv1.Role
					for _, obj := range objs {
						if role, ok := obj.(*rbacv1.Role); ok && role.Namespace == h.systemRegistrationNamespace {
							credRole = role
							break
						}
					}
					Expect(credRole).NotTo(BeNil(), "expected a credential Role in %s", h.systemRegistrationNamespace)
					Expect(credRole.Rules).To(HaveLen(1))
					rule := credRole.Rules[0]
					Expect(rule.Resources).To(ConsistOf("secrets"))
					// ResourceNames MUST be non-empty
					Expect(rule.ResourceNames).NotTo(BeEmpty(), "credential Role must restrict access to a specific secret, not all secrets")
					Expect(rule.Verbs).To(ConsistOf("get"))
				})

				It("creates a scoped RoleBinding in systemRegistrationNamespace", func() {
					objs, _, err := h.OnChange(request, status)
					Expect(err).ToNot(HaveOccurred())

					var credRoleBinding *rbacv1.RoleBinding
					for _, obj := range objs {
						if rb, ok := obj.(*rbacv1.RoleBinding); ok && rb.Namespace == h.systemRegistrationNamespace {
							credRoleBinding = rb
							break
						}
					}
					Expect(credRoleBinding).NotTo(BeNil(), "expected a credential RoleBinding in %s", h.systemRegistrationNamespace)
					Expect(credRoleBinding.Subjects).To(HaveLen(1))
					Expect(credRoleBinding.Subjects[0].Kind).To(Equal("ServiceAccount"))
				})
			})

			Context("tokenCache returns an unexpected error", func() {
				BeforeEach(func() {
					tokenCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, anError)
				})

				It("propagates the error", func() {
					objs, _, err := h.OnChange(request, status)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("failed to look up import token"))
					Expect(objs).To(BeNil())
				})
			})
		})

		When("agent-initiated: RegistrationTokenLabel is set and token exists", func() {
			var agentToken *fleet.ClusterRegistrationToken

			BeforeEach(func() {
				cluster.Status = fleet.ClusterStatus{Namespace: "fleet-default"}
				request.Labels = map[string]string{
					fleet.RegistrationTokenLabel: "my-token",
				}

				sa = &corev1.ServiceAccount{}
				saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(sa, nil)

				secret := &corev1.Secret{Data: map[string][]byte{"token": []byte("secrettoken")}}
				secretController.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(secret, nil)

				clusterRegistrationController.EXPECT().List(gomock.Any(), gomock.Any()).Return(&fleet.ClusterRegistrationList{}, nil)
				clusterRegistrationController.EXPECT().Update(gomock.Any()).DoAndReturn(
					func(cr *fleet.ClusterRegistration) (*fleet.ClusterRegistration, error) { return cr, nil },
				)

				agentToken = &fleet.ClusterRegistrationToken{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-token",
						Namespace: "fleet-default",
						UID:       types.UID("agent-token-uid"),
					},
				}
				tokenCache.EXPECT().Get(gomock.Any(), "import-token-cluster").Return(nil, notFound)
				tokenCache.EXPECT().Get(gomock.Any(), "my-token").Return(agentToken, nil)
			})

			It("grants registration and creates 8 objects including scoped credential RBAC", func() {
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).ToNot(HaveOccurred())
				Expect(objs).To(HaveLen(8))
				Expect(newStatus.Granted).To(BeTrue())

				var credRole *rbacv1.Role
				for _, obj := range objs {
					if role, ok := obj.(*rbacv1.Role); ok && role.Namespace == h.systemRegistrationNamespace {
						credRole = role
						break
					}
				}
				Expect(credRole).NotTo(BeNil(), "expected a credential Role in %s", h.systemRegistrationNamespace)
				Expect(credRole.Rules).To(HaveLen(1))
				Expect(credRole.Rules[0].Resources).To(ConsistOf("secrets"))
				Expect(credRole.Rules[0].ResourceNames).NotTo(BeEmpty(), "credential Role must restrict access to a specific secret")
				Expect(credRole.Rules[0].Verbs).To(ConsistOf("get"))
			})

			It("binds the credential RoleBinding to the agent token's service account", func() {
				objs, _, err := h.OnChange(request, status)
				Expect(err).ToNot(HaveOccurred())

				var credRoleBinding *rbacv1.RoleBinding
				for _, obj := range objs {
					if rb, ok := obj.(*rbacv1.RoleBinding); ok && rb.Namespace == h.systemRegistrationNamespace {
						credRoleBinding = rb
						break
					}
				}
				Expect(credRoleBinding).NotTo(BeNil(), "expected a credential RoleBinding in %s", h.systemRegistrationNamespace)
				Expect(credRoleBinding.Subjects).To(HaveLen(1))
				subject := credRoleBinding.Subjects[0]
				Expect(subject.Kind).To(Equal("ServiceAccount"))
				Expect(subject.Name).To(Equal(names.SafeConcatName("my-token", string(agentToken.UID))))
				Expect(subject.Namespace).To(Equal("fleet-default"))
			})
		})

		When("agent-initiated: RegistrationTokenLabel is set but token has expired", func() {
			BeforeEach(func() {
				cluster.Status = fleet.ClusterStatus{Namespace: "fleet-default"}
				request.Labels = map[string]string{
					fleet.RegistrationTokenLabel: "my-token",
				}

				sa = &corev1.ServiceAccount{}
				saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(sa, nil)

				secret := &corev1.Secret{Data: map[string][]byte{"token": []byte("secrettoken")}}
				secretController.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(secret, nil)

				clusterRegistrationController.EXPECT().List(gomock.Any(), gomock.Any()).Return(&fleet.ClusterRegistrationList{}, nil)
				clusterRegistrationController.EXPECT().Update(gomock.Any()).DoAndReturn(
					func(cr *fleet.ClusterRegistration) (*fleet.ClusterRegistration, error) { return cr, nil },
				)

				tokenCache.EXPECT().Get(gomock.Any(), "import-token-cluster").Return(nil, notFound)
				tokenCache.EXPECT().Get(gomock.Any(), "my-token").Return(nil, notFound)
			})

			It("grants registration with base objects only and no credential RBAC, without error", func() {
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).ToNot(HaveOccurred())
				Expect(newStatus.Granted).To(BeTrue())
				Expect(objs).To(HaveLen(6))

				for _, obj := range objs {
					if m, ok := obj.(metav1.Object); ok {
						if _, isRole := obj.(*rbacv1.Role); isRole {
							Expect(m.GetNamespace()).NotTo(Equal(h.systemRegistrationNamespace),
								"no credential Role should be created when the registration token has expired")
						}
						if _, isRB := obj.(*rbacv1.RoleBinding); isRB {
							Expect(m.GetNamespace()).NotTo(Equal(h.systemRegistrationNamespace),
								"no credential RoleBinding should be created when the registration token has expired")
						}
					}
				}
			})
		})
	})
})
