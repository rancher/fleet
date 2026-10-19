package clusterregistration

import (
	"errors"
	"time"

	"github.com/rancher/wrangler/v3/pkg/generic"
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/registration"
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
		crCache                       *fake.MockCacheInterface[*fleet.ClusterRegistration]
		tokenCache                    *fake.MockCacheInterface[*fleet.ClusterRegistrationToken]
		recorder                      *record.FakeRecorder
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
		crCache = fake.NewMockCacheInterface[*fleet.ClusterRegistration](ctrl)
		clusterRegistrationController.EXPECT().Cache().Return(crCache).AnyTimes()
		recorder = record.NewFakeRecorder(10)

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
			recorder:                    recorder,
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

	Context("Cluster cache lookup by name fails", func() {
		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				Spec: fleet.ClusterRegistrationSpec{
					ClientID: "client-id",
				},
			}
			status = fleet.ClusterRegistrationStatus{}

			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return(nil, nil)
			clusterCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errors.New("cache failure"))
		})

		It("returns the error", func() {
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).To(MatchError("cache failure"))
			Expect(objs).To(BeEmpty())
			Expect(newStatus.Granted).To(BeFalse())
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
						Expect(cluster.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, ""))
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

		When("cluster creation works, request carries a registration token", func() {
			BeforeEach(func() {
				request.Labels = map[string]string{
					fleet.RegistrationTokenLabel: "some-token",
				}
				clusterClient.EXPECT().Create(gomock.Any()).Return(nil, nil).Do(func(obj any) {
					switch cluster := obj.(type) {
					case *fleet.Cluster:
						Expect(cluster.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, "some-token"))
					default:
						Fail("unexpected type")
					}
				})
			})

			It("records the token on the created cluster", func() {
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

		When("another request creates the cluster first", func() {
			BeforeEach(func() {
				request.Labels = map[string]string{fleet.RegistrationTokenLabel: "second-token"}
				clusterClient.EXPECT().Create(gomock.Any()).Return(nil, apierrors.NewAlreadyExists(schema.GroupResource{}, "cluster"))
			})

			It("checks the existing registrant", func() {
				clusterClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(&fleet.Cluster{
					ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{fleet.RegisteredTokenAnnotation: "first-token"}},
					Spec:       fleet.ClusterSpec{ClientID: "client-id"},
				}, nil)
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).To(Equal(generic.ErrSkip))
				Expect(objs).To(BeEmpty())
				Expect(newStatus.Granted).To(BeFalse())
			})

			It("checks the existing ClientID", func() {
				clusterClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(&fleet.Cluster{
					Spec: fleet.ClusterSpec{ClientID: "other-client-id"},
				}, nil)
				_, _, err := h.OnChange(request, status)
				Expect(err).To(MatchError(ContainSubstring("non-matching ClientID")))
			})
		})
	})

	Context("Cluster already registered by a different registration token", func() {
		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-request",
					Namespace: "fleet-default",
					Labels: map[string]string{
						fleet.RegistrationTokenLabel: "other-token",
					},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "registered-client-id",
					ClientRandom: "other-random",
				},
			}
			status = fleet.ClusterRegistrationStatus{}

			registeredCluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-registered",
					Namespace: "fleet-default",
					Annotations: map[string]string{
						fleet.RegisteredTokenAnnotation: "registered-token",
					},
				},
				Spec: fleet.ClusterSpec{
					ClientID: "registered-client-id",
				},
				Status: fleet.ClusterStatus{
					Namespace: "cluster-fleet-default-registered",
				},
			}

			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return([]*fleet.Cluster{registeredCluster}, nil)
			clusterClient.EXPECT().Get("fleet-default", "cluster-registered", gomock.Any()).Return(registeredCluster, nil)
		})

		It("does not grant the registration", func() {
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).To(Equal(generic.ErrSkip))
			Expect(objs).To(BeEmpty())
			Expect(newStatus.Granted).To(BeFalse())
			Expect(recorder.Events).To(Receive(And(
				ContainSubstring("RegistrationTokenMismatch"),
				ContainSubstring(fleet.RegisteredTokenAnnotation+"=other-token"),
			)))
		})
	})

	Context("Cluster already registered with the same registration token", func() {
		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-request",
					Namespace: "fleet-default",
					Labels: map[string]string{
						fleet.RegistrationTokenLabel: "registered-token",
					},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "registered-client-id",
					ClientRandom: "agent-random",
				},
			}
			status = fleet.ClusterRegistrationStatus{}

			existingCluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-registered",
					Namespace: "fleet-default",
					Annotations: map[string]string{
						fleet.RegisteredTokenAnnotation: "registered-token",
					},
				},
				Spec: fleet.ClusterSpec{
					ClientID: "registered-client-id",
				},
			}

			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return([]*fleet.Cluster{existingCluster}, nil)
		})

		It("allows the registration to proceed", func() {
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(objs).To(BeEmpty())
			Expect(newStatus.ClusterName).To(Equal("cluster-registered"))
			Expect(newStatus.Granted).To(BeFalse())
		})
	})

	Context("Imported cluster has no recorded registration token", func() {
		var importedCluster *fleet.Cluster

		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-request",
					Namespace: "fleet-default",
					Labels: map[string]string{
						fleet.RegistrationTokenLabel: "some-token",
					},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "imported-client-id",
					ClientRandom: "agent-random",
				},
			}
			status = fleet.ClusterRegistrationStatus{}

			importedCluster = &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-imported",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					ClientID:         "imported-client-id",
					KubeConfigSecret: "imported-kubeconfig",
				},
			}

			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return([]*fleet.Cluster{importedCluster}, nil)
		})

		It("is not blocked", func() {
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(objs).To(BeEmpty())
			Expect(newStatus.ClusterName).To(Equal("cluster-imported"))
			Expect(newStatus.Granted).To(BeFalse())
		})

		It("ignores a token recorded before the cluster got kubeConfigSecret", func() {
			importedCluster.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "agent-token"}
			request.Labels[fleet.RegistrationTokenLabel] = "import-token-cluster-imported"
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(objs).To(BeEmpty())
			Expect(newStatus.ClusterName).To(Equal("cluster-imported"))
			Expect(newStatus.Granted).To(BeFalse())
		})
	})

	Context("Agent-initiated cluster registered before the token was recorded", func() {
		var (
			currentCluster *fleet.Cluster
			updateErr      error
			history        []*fleet.ClusterRegistration
			registration   func(name, token string, granted bool, created time.Time) *fleet.ClusterRegistration
			now            time.Time
		)
		BeforeEach(func() {
			updateErr = nil
			status = fleet.ClusterRegistrationStatus{}
			legacyCluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-legacy", Namespace: "fleet-default", ResourceVersion: "1"},
				Spec:       fleet.ClusterSpec{ClientID: "legacy-client-id"},
			}
			currentCluster = legacyCluster.DeepCopy()
			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return([]*fleet.Cluster{legacyCluster}, nil)
			clusterClient.EXPECT().Get("fleet-default", "cluster-legacy", gomock.Any()).DoAndReturn(
				func(string, string, metav1.GetOptions) (*fleet.Cluster, error) { return currentCluster, nil },
			)
			clusterClient.EXPECT().Update(gomock.Any()).DoAndReturn(
				func(cluster *fleet.Cluster) (*fleet.Cluster, error) {
					Expect(cluster.ResourceVersion).To(Equal("1"))
					Expect(cluster.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, "current-token"))
					Expect(legacyCluster.Annotations).To(BeNil())
					return cluster, updateErr
				},
			).MaxTimes(1)

			registration = func(name, token string, granted bool, created time.Time) *fleet.ClusterRegistration {
				return &fleet.ClusterRegistration{
					ObjectMeta: metav1.ObjectMeta{
						Name:              name,
						Namespace:         "fleet-default",
						Labels:            map[string]string{fleet.RegistrationTokenLabel: token},
						CreationTimestamp: metav1.NewTime(created),
					},
					Status: fleet.ClusterRegistrationStatus{Granted: granted},
				}
			}
			now = time.Now().Truncate(time.Second)
			history = []*fleet.ClusterRegistration{
				registration("older", "old-token", true, now.Add(-2*time.Hour)),
				registration("latest", "current-token", true, now.Add(-time.Hour)),
				registration("pending", "pending-token", false, now),
			}
			crCache.EXPECT().GetByIndex(clusterRegistrationByClientID, "fleet-default/legacy-client-id").DoAndReturn(
				func(string, string) ([]*fleet.ClusterRegistration, error) { return history, nil },
			)

			request = &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{Name: "new-request", Namespace: "fleet-default"},
				Spec:       fleet.ClusterRegistrationSpec{ClientID: "legacy-client-id", ClientRandom: "new-random"},
			}
		})

		It("does not grant a token other than the one of the latest granted registration", func() {
			request.Labels = map[string]string{fleet.RegistrationTokenLabel: "old-token"}
			_, newStatus, err := h.OnChange(request, status)
			Expect(err).To(Equal(generic.ErrSkip))
			Expect(newStatus.Granted).To(BeFalse())
			Expect(recorder.Events).To(Receive(ContainSubstring(`registered with "current-token"`)))
		})

		It("allows the token of the latest granted registration", func() {
			request.Labels = map[string]string{fleet.RegistrationTokenLabel: "current-token"}
			_, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(newStatus.ClusterName).To(Equal("cluster-legacy"))
		})

		It("does not proceed when persisting the token conflicts with a Cluster update", func() {
			updateErr = apierrors.NewConflict(schema.GroupResource{Resource: "clusters"}, "cluster-legacy", anError)
			request.Labels = map[string]string{fleet.RegistrationTokenLabel: "current-token"}
			objs, newStatus, err := h.OnChange(request, status)
			Expect(apierrors.IsConflict(err)).To(BeTrue())
			Expect(objs).To(BeEmpty())
			Expect(newStatus.Granted).To(BeFalse())
		})

		It("honors the live binding when the Cluster cache has not observed it yet", func() {
			currentCluster.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "selected-token"}
			request.Labels = map[string]string{fleet.RegistrationTokenLabel: "current-token"}
			_, newStatus, err := h.OnChange(request, status)
			Expect(err).To(Equal(generic.ErrSkip))
			Expect(newStatus.Granted).To(BeFalse())
			Expect(recorder.Events).To(Receive(ContainSubstring(`registered with "selected-token"`)))
		})

		It("neither binds nor rejects when the live Cluster has kubeConfigSecret", func() {
			currentCluster.Spec.KubeConfigSecret = "kubeconfig"
			request.Labels = map[string]string{fleet.RegistrationTokenLabel: "import-token-cluster-legacy"}
			_, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(newStatus.ClusterName).To(Equal("cluster-legacy"))
			Expect(recorder.Events).NotTo(Receive())
		})

		It("allows the token of registrations granted within the same latest second", func() {
			history = append(history, registration("latest-again", "current-token", true, now.Add(-time.Hour)))
			request.Labels = map[string]string{fleet.RegistrationTokenLabel: "current-token"}
			_, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(newStatus.ClusterName).To(Equal("cluster-legacy"))
		})

		DescribeTable("does not grant or record any token when same-second registrations used different tokens",
			func(token string) {
				history = append(history, registration("tied", "tied-token", true, now.Add(-time.Hour)))
				request.Labels = map[string]string{fleet.RegistrationTokenLabel: token}
				_, newStatus, err := h.OnChange(request, status)
				Expect(err).To(Equal(generic.ErrSkip))
				Expect(newStatus.Granted).To(BeFalse())
				Expect(recorder.Events).To(Receive(ContainSubstring(`registered with "current-token,tied-token"`)))
			},
			Entry("first token", "current-token"),
			Entry("second token", "tied-token"),
		)
	})

	Context("Cached Cluster records another token, but the live Cluster changed", func() {
		var live *fleet.Cluster

		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "import-request",
					Namespace: "fleet-default",
					Labels:    map[string]string{fleet.RegistrationTokenLabel: "import-token-cluster-switched"},
				},
				Spec: fleet.ClusterRegistrationSpec{ClientID: "switched-client-id", ClientRandom: "import-random"},
			}
			status = fleet.ClusterRegistrationStatus{}

			cached := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "cluster-switched",
					Namespace:   "fleet-default",
					Annotations: map[string]string{fleet.RegisteredTokenAnnotation: "agent-token"},
				},
				Spec: fleet.ClusterSpec{ClientID: "switched-client-id"},
			}
			live = cached.DeepCopy()
			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return([]*fleet.Cluster{cached}, nil)
			clusterClient.EXPECT().Get("fleet-default", "cluster-switched", gomock.Any()).DoAndReturn(
				func(string, string, metav1.GetOptions) (*fleet.Cluster, error) { return live, nil },
			)
		})

		expectProceeds := func() {
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(objs).To(BeEmpty())
			Expect(newStatus.ClusterName).To(Equal("cluster-switched"))
			Expect(recorder.Events).NotTo(Receive())
		}

		It("proceeds when the live Cluster has kubeConfigSecret", func() {
			live.Spec.KubeConfigSecret = "kubeconfig"
			expectProceeds()
		})

		It("proceeds when the live annotation names the request token", func() {
			live.Annotations[fleet.RegisteredTokenAnnotation] = "import-token-cluster-switched"
			expectProceeds()
		})

		It("reports a mismatch confirmed on the live Cluster", func() {
			_, newStatus, err := h.OnChange(request, status)
			Expect(err).To(Equal(generic.ErrSkip))
			Expect(newStatus.Granted).To(BeFalse())
			Expect(recorder.Events).To(Receive(ContainSubstring(`registered with "agent-token"`)))
		})
	})

	Context("Cluster was first registered without a token, later request presents one", func() {
		BeforeEach(func() {
			request = &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-request",
					Namespace: "fleet-default",
					Labels: map[string]string{
						fleet.RegistrationTokenLabel: "other-token",
					},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "registered-client-id",
					ClientRandom: "other-random",
				},
			}
			status = fleet.ClusterRegistrationStatus{}

			// The cluster's first registration carried no token label, so the
			// annotation was recorded with an empty value, not omitted.
			registeredCluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-registered",
					Namespace: "fleet-default",
					Annotations: map[string]string{
						fleet.RegisteredTokenAnnotation: "",
					},
				},
				Spec: fleet.ClusterSpec{
					ClientID: "registered-client-id",
				},
			}

			clusterCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return([]*fleet.Cluster{registeredCluster}, nil)
			clusterClient.EXPECT().Get("fleet-default", "cluster-registered", gomock.Any()).Return(registeredCluster, nil).MaxTimes(1)
		})

		It("does not grant the registration", func() {
			objs, newStatus, err := h.OnChange(request, status)
			Expect(err).To(Equal(generic.ErrSkip))
			Expect(objs).To(BeEmpty())
			Expect(newStatus.Granted).To(BeFalse())
		})

		It("allows the registration when token changes are allowed", func() {
			prev := config.Get()
			DeferCleanup(config.Set, prev)
			config.Set(&config.Config{AllowClusterRegistrationTokenChange: true})

			_, newStatus, err := h.OnChange(request, status)
			Expect(err).ToNot(HaveOccurred())
			Expect(newStatus.ClusterName).To(Equal("cluster-registered"))
		})
	})

	Context("Cluster exists", func() {
		// liveClusters are returned by successive live Cluster reads; the
		// last one is repeated.
		var (
			liveClusters  []*fleet.Cluster
			liveReadCount int
			liveReadErr   error
		)

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
			crCache.EXPECT().GetByIndex(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			liveClusters = []*fleet.Cluster{cluster}
			liveReadCount, liveReadErr = 0, nil
			clusterClient.EXPECT().Get("fleet-default", "cluster", gomock.Any()).DoAndReturn(
				func(string, string, metav1.GetOptions) (*fleet.Cluster, error) {
					liveReadCount++
					if liveReadErr != nil {
						return nil, liveReadErr
					}
					live := liveClusters[0]
					if len(liveClusters) > 1 {
						liveClusters = liveClusters[1:]
					}
					return live, nil
				},
			).AnyTimes()
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
			var olderRequest fleet.ClusterRegistration

			BeforeEach(func() {
				cluster.Status = fleet.ClusterStatus{Namespace: "fleet-default"}
				request.Labels = map[string]string{fleet.RegistrationTokenLabel: "my-token"}
				request.CreationTimestamp = metav1.Now()

				sa = &corev1.ServiceAccount{}
				saCache.EXPECT().Get(gomock.Any(), gomock.Any()).Return(sa, nil)

				// needs token here, otherwise controller will sleep to wait for it
				secret := &corev1.Secret{Data: map[string][]byte{"token": []byte("secrettoken")}}
				secretController.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(secret, nil)

				clusterRegistrationController.EXPECT().Update(gomock.Any()).DoAndReturn(
					func(cr *fleet.ClusterRegistration) (*fleet.ClusterRegistration, error) { return cr, nil },
				)

				olderRequest = fleet.ClusterRegistration{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "request-0",
						Namespace:         "fleet-default",
						CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
					},
					Spec: fleet.ClusterRegistrationSpec{ClientID: "client-id", ClientRandom: "older-random"},
				}
			})

			expectOlderRequestDeleted := func() {
				clusterRegistrationController.EXPECT().List("fleet-default", gomock.Any()).
					Return(&fleet.ClusterRegistrationList{Items: []fleet.ClusterRegistration{olderRequest}}, nil)
				clusterRegistrationController.EXPECT().Delete("fleet-default", "request-0", gomock.Any()).Return(nil)
			}

			// Older registrations are kept: no List or Delete call is expected.
			expectNotGranted := func() {
				objs, newStatus, err := h.OnChange(request, status)
				Expect(err).ToNot(HaveOccurred())
				Expect(objs).To(ConsistOf(requestSA(names.SafeConcatName(request.Name, string(request.UID)), cluster, request)))
				Expect(newStatus.Granted).To(BeFalse())
				Expect(newStatus.ClusterName).To(Equal("cluster"))
			}

			credentialRBAC := func(objs []runtime.Object) (*rbacv1.Role, *rbacv1.RoleBinding) {
				var (
					role *rbacv1.Role
					rb   *rbacv1.RoleBinding
				)
				for _, obj := range objs {
					switch o := obj.(type) {
					case *rbacv1.Role:
						if o.Namespace == h.systemRegistrationNamespace {
							role = o
						}
					case *rbacv1.RoleBinding:
						if o.Namespace == h.systemRegistrationNamespace {
							rb = o
						}
					}
				}
				Expect(role).NotTo(BeNil(), "expected a credential Role in %s", h.systemRegistrationNamespace)
				Expect(rb).NotTo(BeNil(), "expected a credential RoleBinding in %s", h.systemRegistrationNamespace)
				return role, rb
			}

			Context("agent-initiated: token from the request label exists", func() {
				BeforeEach(func() {
					tokenCache.EXPECT().Get("fleet-default", "my-token").Return(&fleet.ClusterRegistrationToken{
						ObjectMeta: metav1.ObjectMeta{Name: "my-token", Namespace: "fleet-default", UID: types.UID("my-token-uid")},
					}, nil)
				})

				When("the Cluster has no recorded token", func() {
					BeforeEach(func() {
						clusterClient.EXPECT().Update(gomock.Any()).DoAndReturn(func(updated *fleet.Cluster) (*fleet.Cluster, error) {
							Expect(updated.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, "my-token"))
							Expect(cluster.Annotations).To(BeNil(), "the cached Cluster must not be modified")
							return updated, nil
						})
						expectOlderRequestDeleted()
					})

					It("grants registration with credential access scoped to the request's secret for that token", func() {
						objs, newStatus, err := h.OnChange(request, status)
						Expect(err).ToNot(HaveOccurred())
						Expect(objs).To(HaveLen(8))
						Expect(newStatus.Granted).To(BeTrue())

						role, rb := credentialRBAC(objs)
						Expect(role.Name).To(Equal(names.SafeConcatName(request.Name, "creds")))
						Expect(role.Rules).To(HaveLen(1))
						Expect(role.Rules[0].Resources).To(ConsistOf("secrets"))
						Expect(role.Rules[0].ResourceNames).NotTo(BeEmpty(), "credential Role must restrict access to a specific secret")
						Expect(role.Rules[0].Verbs).To(ConsistOf("get"))
						Expect(rb.Subjects).To(ConsistOf(rbacv1.Subject{
							Kind:      "ServiceAccount",
							Name:      names.SafeConcatName("my-token", "my-token-uid"),
							Namespace: "fleet-default",
						}))
					})
				})

				When("another request recorded a different token after validation", func() {
					BeforeEach(func() {
						bound := cluster.DeepCopy()
						bound.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "other-token"}
						liveClusters = []*fleet.Cluster{cluster, bound}
					})

					// No Update, List or Delete call is expected.
					It("neither grants nor replaces older registrations", func() {
						objs, newStatus, err := h.OnChange(request, status)
						var mismatch *registrantMismatchError
						Expect(errors.As(err, &mismatch)).To(BeTrue())
						Expect(mismatch.registered).To(Equal("other-token"))
						Expect(recorder.Events).To(Receive(And(
							ContainSubstring("Warning RegistrationTokenMismatch"),
							ContainSubstring(`uses registration token "my-token"`),
							ContainSubstring(`registered with "other-token"`),
						)))
						Expect(objs).To(BeEmpty())
						Expect(newStatus.Granted).To(BeFalse())
					})
				})

				When("the cached Cluster records the token", func() {
					BeforeEach(func() {
						cluster.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "my-token"}
					})

					When("the live Cluster records it too", func() {
						BeforeEach(expectOlderRequestDeleted)

						// No Update call is expected.
						It("grants registration", func() {
							_, newStatus, err := h.OnChange(request, status)
							Expect(err).ToNot(HaveOccurred())
							Expect(newStatus.Granted).To(BeTrue())
						})
					})

					When("the live Cluster was rebound to another token", func() {
						BeforeEach(func() {
							rebound := cluster.DeepCopy()
							rebound.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "other-token"}
							liveClusters = []*fleet.Cluster{rebound}
						})

						// No Update, List or Delete call is expected.
						It("neither grants nor replaces older registrations", func() {
							objs, newStatus, err := h.OnChange(request, status)
							var mismatch *registrantMismatchError
							Expect(errors.As(err, &mismatch)).To(BeTrue())
							Expect(mismatch.registered).To(Equal("other-token"))
							Expect(recorder.Events).To(Receive(And(
								ContainSubstring("Warning RegistrationTokenMismatch"),
								ContainSubstring(`uses registration token "my-token"`),
								ContainSubstring(`registered with "other-token"`),
							)))
							Expect(objs).To(BeEmpty())
							Expect(newStatus.Granted).To(BeFalse())
						})
					})
				})

				When("the cached Cluster has kubeConfigSecret but the live one is bound to another token", func() {
					BeforeEach(func() {
						cluster.Spec.KubeConfigSecret = "cached-kubeconfig"
						cluster.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "other-token"}
						live := cluster.DeepCopy()
						live.Spec.KubeConfigSecret = ""
						liveClusters = []*fleet.Cluster{live}
					})

					// No Update, List or Delete call is expected.
					It("neither grants nor replaces older registrations", func() {
						objs, newStatus, err := h.OnChange(request, status)
						var mismatch *registrantMismatchError
						Expect(errors.As(err, &mismatch)).To(BeTrue())
						Expect(mismatch.registered).To(Equal("other-token"))
						Expect(objs).To(BeEmpty())
						Expect(newStatus.Granted).To(BeFalse())
					})
				})
			})

			DescribeTable("uses one live Cluster snapshot after the registration mode changes",
				func(wasImported bool) {
					if wasImported {
						cluster.Spec.KubeConfigSecret = "cached-kubeconfig"
					} else {
						cluster.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "my-token"}
					}
					current := cluster.DeepCopy()
					current.ResourceVersion = "2"
					current.Annotations = nil
					selected := "my-token"
					if wasImported {
						current.Spec.KubeConfigSecret = ""
						clusterClient.EXPECT().Update(gomock.Any()).DoAndReturn(func(updated *fleet.Cluster) (*fleet.Cluster, error) {
							Expect(updated.ResourceVersion).To(Equal("2"))
							Expect(updated.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, "my-token"))
							Expect(current.Annotations).To(BeNil())
							return updated, nil
						})
					} else {
						current.Spec.KubeConfigSecret = "live-kubeconfig"
						selected = "import-token-cluster"
					}
					liveClusters = []*fleet.Cluster{current}
					tokenCache.EXPECT().Get("fleet-default", selected).Return(&fleet.ClusterRegistrationToken{
						ObjectMeta: metav1.ObjectMeta{Name: selected, Namespace: "fleet-default", UID: types.UID("selected-uid")},
					}, nil)
					expectOlderRequestDeleted()

					objs, newStatus, err := h.OnChange(request, status)
					Expect(err).ToNot(HaveOccurred())
					Expect(newStatus.Granted).To(BeTrue())
					Expect(liveReadCount).To(Equal(1))
					role, binding := credentialRBAC(objs)
					Expect(role.Rules[0].ResourceNames).To(ConsistOf(registration.SecretName(request.Spec.ClientID, request.Spec.ClientRandom)))
					Expect(binding.Subjects).To(ConsistOf(rbacv1.Subject{
						Kind: "ServiceAccount", Name: names.SafeConcatName(selected, "selected-uid"), Namespace: "fleet-default",
					}))
				},
				Entry("from manager-initiated to agent-initiated", true),
				Entry("from agent-initiated to manager-initiated", false),
			)

			Context("live Cluster lookup fails before the grant", func() {
				BeforeEach(func() {
					cluster.Annotations = map[string]string{fleet.RegisteredTokenAnnotation: "my-token"}
					liveReadErr = anError
				})

				// No token lookup, binding update or old registration deletion is expected.
				It("propagates the error and keeps older registrations", func() {
					objs, newStatus, err := h.OnChange(request, status)
					Expect(errors.Is(err, anError)).To(BeTrue())
					Expect(objs).To(BeEmpty())
					Expect(newStatus.Granted).To(BeFalse())
				})
			})

			Context("agent-initiated: request has no token label", func() {
				BeforeEach(func() {
					request.Labels = nil
				})

				It("does not grant registration", expectNotGranted)
			})

			Context("agent-initiated: token from the request label does not exist", func() {
				BeforeEach(func() {
					tokenCache.EXPECT().Get("fleet-default", "my-token").Return(nil, notFound)
				})

				It("does not grant registration", expectNotGranted)
			})

			Context("token lookup fails", func() {
				BeforeEach(func() {
					tokenCache.EXPECT().Get("fleet-default", "my-token").Return(nil, anError)
				})

				It("propagates the error", func() {
					objs, newStatus, err := h.OnChange(request, status)
					Expect(err).To(MatchError(ContainSubstring("looking up registration token fleet-default/my-token")))
					Expect(objs).To(BeEmpty())
					Expect(newStatus.Granted).To(BeFalse())
				})
			})

			Context("manager-initiated: import token exists", func() {
				BeforeEach(func() {
					cluster.Spec.KubeConfigSecret = "kubeconfig"
					tokenCache.EXPECT().Get("fleet-default", "import-token-cluster").Return(&fleet.ClusterRegistrationToken{
						ObjectMeta: metav1.ObjectMeta{Name: "import-token-cluster", Namespace: "fleet-default", UID: types.UID("import-uid")},
					}, nil)
					expectOlderRequestDeleted()
				})

				It("grants credential access to the import token, not to the token from the request label", func() {
					objs, newStatus, err := h.OnChange(request, status)
					Expect(err).ToNot(HaveOccurred())
					Expect(objs).To(HaveLen(8))
					Expect(newStatus.Granted).To(BeTrue())

					importSAName := names.SafeConcatName("import-token-cluster", "import-uid")
					role, rb := credentialRBAC(objs)
					Expect(role.Name).To(Equal(names.SafeConcatName(importSAName, "creds")))
					Expect(role.Rules).To(HaveLen(1))
					Expect(role.Rules[0].ResourceNames).NotTo(BeEmpty(), "credential Role must restrict access to a specific secret")
					Expect(rb.Subjects).To(ConsistOf(rbacv1.Subject{
						Kind:      "ServiceAccount",
						Name:      importSAName,
						Namespace: "fleet-default",
					}))
				})
			})

			Context("manager-initiated: import token does not exist", func() {
				BeforeEach(func() {
					cluster.Spec.KubeConfigSecret = "kubeconfig"
					tokenCache.EXPECT().Get("fleet-default", "import-token-cluster").Return(nil, notFound)
				})

				It("does not grant registration to the token from the request label", expectNotGranted)
			})
		})
	})

	Context("pending registration wakeups", func() {
		BeforeEach(func() {
			request = &fleet.ClusterRegistration{ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "fleet-default"}}
		})

		expectPendingEnqueued := func(namespace string) {
			granted := request.DeepCopy()
			granted.Name = "granted"
			granted.Status.Granted = true
			external := request.DeepCopy()
			external.Name = "external"
			external.Labels = map[string]string{fleet.ClusterManagementLabel: "other-manager"}
			crCache.EXPECT().List(namespace, labels.Everything()).Return([]*fleet.ClusterRegistration{request, granted, external}, nil)
			clusterRegistrationController.EXPECT().Enqueue("fleet-default", "pending")
		}

		It("enqueues pending registrations when token changes are enabled", func() {
			expectPendingEnqueued(metav1.NamespaceAll)
			Expect(h.OnConfig(&config.Config{AllowClusterRegistrationTokenChange: true})).To(Succeed())
		})

		It("does not enqueue registrations when token changes remain disabled", func() {
			Expect(h.OnConfig(&config.Config{})).To(Succeed())
		})

		It("enqueues pending registrations in the namespace of a changed token", func() {
			expectPendingEnqueued("fleet-default")
			token := &fleet.ClusterRegistrationToken{ObjectMeta: metav1.ObjectMeta{Namespace: "fleet-default", Name: "selected-token"}}
			actual, err := h.OnTokenChange("", token)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal(token))
		})

		It("propagates cache errors to the config reload", func() {
			crCache.EXPECT().List(metav1.NamespaceAll, labels.Everything()).Return(nil, anError)
			Expect(h.OnConfig(&config.Config{AllowClusterRegistrationTokenChange: true})).To(MatchError(anError))
		})

		It("does nothing for a deleted token", func() {
			actual, err := h.OnTokenChange("", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(BeNil())
		})
	})
})
