package doctor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"gopkg.in/yaml.v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Fleet doctor report", func() {
	var (
		ctrl          *gomock.Controller
		fakeClient    *mocks.MockK8sClient
		fakeDynClient *fake.FakeDynamicClient
		objs          []runtime.Object
	)

	JustBeforeEach(func() {
		fakeDynClient = fake.NewSimpleDynamicClient(scheme, objs...)

		ctrl = gomock.NewController(GinkgoT())
		fakeClient = mocks.NewMockK8sClient(ctrl)
	})

	When("the cluster contains items of each supported resource type", func() {
		BeforeEach(func() {
			// Comparisons will fail if TypeMeta is not explicitly populated
			testGitRepo := fleet.GitRepo{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GitRepo",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-gitrepo",
					Namespace: "foo",
				},
			}

			testBundle := fleet.Bundle{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Bundle",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bundle",
					Namespace: "bar",
				},
			}

			testBundleDeployment := fleet.BundleDeployment{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Bundledeployment",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bundledeployment",
					Namespace: "baz",
				},
			}

			testHelmOp := fleet.HelmOp{
				TypeMeta: metav1.TypeMeta{
					Kind:       "HelmOp",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-helmop",
					Namespace: "hey",
				},
			}

			testbnm := fleet.BundleNamespaceMapping{
				TypeMeta: metav1.TypeMeta{
					Kind:       "BundleNamespaceMapping",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bnm",
					Namespace: "test-bnm",
				},
			}

			testgrr := fleet.GitRepoRestriction{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GitRepoRestriction",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-grr",
					Namespace: "test-grr",
				},
			}

			testCluster := fleet.Cluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Cluster",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cluster",
					Namespace: "fleet-local",
				},
			}

			testClusterGroup := fleet.ClusterGroup{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterGroup",
					APIVersion: "fleet.cattle.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cg",
					Namespace: "test-cg",
				},
			}

			objs = []runtime.Object{
				&testGitRepo,
				&testBundle,
				&testBundleDeployment,
				&testHelmOp,
				&testbnm,
				&testgrr,
				&testCluster,
				&testClusterGroup,
			}

			DeferCleanup(func() {
				objs = nil // prevent conflicts with other test cases
			})
		})

		It("returns an archive containing all of these resources", func() {
			tgzPath := "test.tgz"

			// Listing events (covered in another test case)
			fakeClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			err := fleetDoctor(fakeDynClient, fakeClient, tgzPath)
			Expect(err).ToNot(HaveOccurred())

			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			f, err := os.OpenFile(tgzPath, os.O_RDONLY, 0)
			Expect(err).ToNot(HaveOccurred())

			defer f.Close()

			gzr, err := gzip.NewReader(f)
			Expect(err).ToNot(HaveOccurred())

			tr := tar.NewReader(gzr)

			foundObjs := []runtime.Object{}
			for {
				header, err := tr.Next()
				if errors.Is(err, io.EOF) {
					break
				}

				Expect(err).ToNot(HaveOccurred())
				Expect(int32(header.Typeflag)).To(Equal(tar.TypeReg)) // regular file

				content, err := io.ReadAll(tr)
				Expect(err).ToNot(HaveOccurred())

				fileName := strings.Split(header.Name, "_")
				Expect(fileName).To(HaveLen(2))

				kindLow := fileName[0]

				var tmp map[string]any
				err = yaml.Unmarshal(content, &tmp)
				Expect(err).ToNot(HaveOccurred())

				switch kindLow {
				case "bundles":
					var b struct {
						Object fleet.Bundle `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "bundledeployments":
					var b struct {
						Object fleet.BundleDeployment `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "gitrepos":
					var b struct {
						Object fleet.GitRepo `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "helmops":
					var b struct {
						Object fleet.HelmOp `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "bundlenamespacemappings":
					var b struct {
						Object fleet.BundleNamespaceMapping `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "gitreporestrictions":
					var b struct {
						Object fleet.GitRepoRestriction `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "clusters":
					var b struct {
						Object fleet.Cluster `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "clustergroups":
					var b struct {
						Object fleet.ClusterGroup `json:"object"`
					}
					err = runtime.DefaultUnstructuredConverter.FromUnstructured(tmp, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				}
			}

			Expect(foundObjs).To(HaveLen(len(objs)))
			for _, eo := range objs {
				found := false
				for _, ao := range foundObjs {
					if reflect.DeepEqual(ao, eo) {
						found = true
					}
				}
				Expect(found).To(BeTrue(), fmt.Sprintf("object %s not found", eo))
			}
		})
	})

	When("the cluster contains events from multiple namespaces", func() {
		It("returns an archive containing all of these events", func() {
			tgzPath := "test_events.tgz"

			nss := []string{"cattle-fleet-system", "default", "cattle-fleet-local-system", "kube-system"}
			nsWithNoEvents := "cattle-fleet-local-system"
			for _, ns := range nss {
				fakeClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.EventList{}), client.InNamespace(ns)).
					DoAndReturn(
						func(_ context.Context, evts *corev1.EventList, _ ...client.ListOption) error {
							if ns == nsWithNoEvents {
								return nil // Test absence of file if no events exist.
							}

							evts.Items = []corev1.Event{
								{
									ObjectMeta: metav1.ObjectMeta{
										Namespace: ns,
									},
									Reason:         "reason-1",
									FirstTimestamp: metav1.Time{Time: time.Now()},
								},
								{
									ObjectMeta: metav1.ObjectMeta{
										Namespace: ns,
									},
									Reason:         "reason-2",
									FirstTimestamp: metav1.Time{Time: time.Now()},
								},
							}

							return nil
						})
			}

			fakeClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.ServiceList{}), gomock.Any()).
				Return(nil)

			err := fleetDoctor(fakeDynClient, fakeClient, tgzPath)
			Expect(err).ToNot(HaveOccurred())

			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			f, err := os.OpenFile(tgzPath, os.O_RDONLY, 0)
			Expect(err).ToNot(HaveOccurred())

			defer f.Close()

			gzr, err := gzip.NewReader(f)
			Expect(err).ToNot(HaveOccurred())

			tr := tar.NewReader(gzr)

			foundEventsByNS := map[string][][]byte{}
			for {
				header, err := tr.Next()
				if errors.Is(err, io.EOF) {
					break
				}

				Expect(err).ToNot(HaveOccurred())
				Expect(int32(header.Typeflag)).To(Equal(tar.TypeReg)) // regular file

				content, err := io.ReadAll(tr)
				Expect(err).ToNot(HaveOccurred())

				fileName := strings.Split(header.Name, "_")
				Expect(fileName).To(HaveLen(2))

				kindLow := fileName[0]

				if kindLow == "events" {
					ns := fileName[1]
					foundEventsByNS[ns] = [][]byte{}

					for e := range bytes.SplitSeq(content, []byte("\n")) {
						foundEventsByNS[ns] = append(foundEventsByNS[ns], e)
					}

					// DEBUG
					GinkgoWriter.Printf("[%s] Found events: %q\n", ns, content)
				}
			}

			Expect(foundEventsByNS).To(HaveLen(len(nss) - 1)) // no events in cattle-fleet-local-system
			for _, ns := range nss {
				if ns == nsWithNoEvents {
					Expect(maps.Keys(foundEventsByNS)).NotTo(ContainElement(ns))
					continue
				}

				// Check that event files are written with one event per line
				Expect(foundEventsByNS[ns]).To(HaveLen(2))
				for i, v := range foundEventsByNS[ns] {
					var e corev1.Event
					Expect(json.Unmarshal(v, &e)).ToNot(HaveOccurred())
					Expect(e.Reason).To(Equal(fmt.Sprintf("reason-%d", i+1)))
				}
			}
		})
	})

	When("the cluster has Fleet installed with metrics enabled", func() {
		It("includes metrics into the archive", func() {
			// TODO
		})
	})
})
