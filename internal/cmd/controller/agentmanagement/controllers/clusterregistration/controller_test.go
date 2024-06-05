package clusterregistration

import (
	"fmt"

	"github.com/golang/mock/gomock"
	"github.com/rancher/wrangler/v3/pkg/generic"
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

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
		h                             *handler
		notFound                      = errors.NewNotFound(schema.GroupResource{}, "")
		anError                       = fmt.Errorf("an error occurred")
	)

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		saCache = fake.NewMockCacheInterface[*corev1.ServiceAccount](ctrl)
		secretCache = fake.NewMockCacheInterface[*corev1.Secret](ctrl)
		secretController = fake.NewMockControllerInterface[*corev1.Secret, *corev1.SecretList](ctrl)
		clusterClient = fake.NewMockClientInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
		clusterRegistrationController = fake.NewMockControllerInterface[*fleet.ClusterRegistration, *fleet.ClusterRegistrationList](ctrl)
		clusterCache = fake.NewMockCacheInterface[*fleet.Cluster](ctrl)

		h = &handler{
			systemNamespace:             "fleet-system",
			systemRegistrationNamespace: "fleet-clusters-system",
			clusterRegistration:         clusterRegistrationController,
			clusterCache:                clusterCache,
			clusters:                    clusterClient,
			secretsCache:                secretCache,
			secrets:                     secretController,
			serviceAccountCache:         saCache,
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
				clusterClient.EXPECT().Create(gomock.Any()).Return(nil, nil).Do(func(obj interface{}) {
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

			Context("grants registration, cleans up and creates objects", func() {
				BeforeEach(func() {
				})

				It("creates a new secret", func() {
					objs, newStatus, err := h.OnChange(request, status)
					Expect(err).ToNot(HaveOccurred())
					Expect(objs).To(HaveLen(6))
					Expect(newStatus.Granted).To(BeTrue())
				})
			})
		})
	})
})
