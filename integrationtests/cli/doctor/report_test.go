package doctor

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Fleet doctor report", func() {
	var (
		fakeClient *fake.FakeDynamicClient
		objs       []runtime.Object
	)

	JustBeforeEach(func() {
		fakeClient = fake.NewSimpleDynamicClient(scheme, objs...)
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
		})

		It("returns an archive containing all of these resources", func() {
			tgzPath := "test.tgz"

			err := fleetDoctor(fakeClient, tgzPath)
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
				if err == io.EOF {
					break
				}

				Expect(err).ToNot(HaveOccurred())
				Expect(int32(header.Typeflag)).To(Equal(tar.TypeReg)) // regular file

				content, err := io.ReadAll(tr)
				Expect(err).ToNot(HaveOccurred())

				var tmp map[string]any
				err = yaml.Unmarshal(content, &tmp)
				Expect(err).ToNot(HaveOccurred())

				fileName := strings.Split(header.Name, "_")
				Expect(fileName).To(HaveLen(2))

				kindLow := fileName[0]
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
})
