package dump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Fleet dump", func() {
	var (
		objs       []client.Object
		namespaces []string
	)

	JustBeforeEach(func() {
		namespaces = []string{}
		for _, o := range objs {
			ns := o.GetNamespace()
			if !slices.Contains(namespaces, ns) {
				mustCreateNS(ns)
				namespaces = append(namespaces, ns)
			}

			err := k8sClient.Create(ctx, o)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to create %s/%s: %v", o.GetNamespace(), o.GetName(), err))
		}

		DeferCleanup(func() {
			for _, o := range objs {
				Expect(k8sClient.Delete(ctx, o)).NotTo(HaveOccurred())
			}

			objs = nil
		})
	})

	When("the cluster contains items of each supported resource type", func() {
		BeforeEach(func() {
			testGitRepo := fleet.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-gitrepo",
					Namespace: "foo",
				},
				Spec: fleet.GitRepoSpec{
					Repo: "http://example.com/myrepo", // not evaluated, but must be non-empty
				},
			}

			testBundle := fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bundle",
					Namespace: "bar",
				},
			}

			testBundleDeployment := fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bundledeployment",
					Namespace: "baz",
				},
			}

			testHelmOp := fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-helmop",
					Namespace: "hey",
				},
			}

			testbnm := fleet.BundleNamespaceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bnm",
					Namespace: "test-bnm",
				},
			}

			testgrr := fleet.GitRepoRestriction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-grr",
					Namespace: "test-grr",
				},
			}

			testCluster := fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cluster",
					Namespace: "fleet-local",
				},
			}

			testClusterGroup := fleet.ClusterGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cg",
					Namespace: "test-cg",
				},
			}

			objs = []client.Object{
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

			err := fleetDump(tgzPath)
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

			foundObjs := []client.Object{}
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

				switch kindLow {
				case "bundles":
					var b struct {
						Object fleet.Bundle `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "bundledeployments":
					var b struct {
						Object fleet.BundleDeployment `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "gitrepos":
					var b struct {
						Object fleet.GitRepo `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "helmops":
					var b struct {
						Object fleet.HelmOp `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "bundlenamespacemappings":
					var b struct {
						Object fleet.BundleNamespaceMapping `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "gitreporestrictions":
					var b struct {
						Object fleet.GitRepoRestriction `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "clusters":
					var b struct {
						Object fleet.Cluster `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				case "clustergroups":
					var b struct {
						Object fleet.ClusterGroup `json:"object"`
					}
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b.Object)
				}
			}

			Expect(foundObjs).To(HaveLen(len(objs)))
			for _, eo := range objs {
				found := false
				for _, ao := range foundObjs {
					if areEqual(ao, eo) {
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
				if ns == nsWithNoEvents {
					continue // Test absence of file if no events exist.
				}

				mustCreateNS(ns)

				evts := []corev1.Event{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: ns,
							Name:      "event1",
						},
						InvolvedObject: corev1.ObjectReference{
							Namespace: ns,
						},
						Reason:         "reason-1",
						FirstTimestamp: metav1.Time{Time: time.Now()},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: ns,
							Name:      "event2",
						},
						InvolvedObject: corev1.ObjectReference{
							Namespace: ns,
						},
						Reason:         "reason-2",
						FirstTimestamp: metav1.Time{Time: time.Now()},
					},
				}

				for _, e := range evts {
					Expect(k8sClient.Create(ctx, &e)).NotTo(HaveOccurred())
				}
			}

			err := fleetDump(tgzPath)
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

				Expect(kindLow).To(Equal("events"))
				ns := fileName[1]
				foundEventsByNS[ns] = [][]byte{}

				for e := range bytes.SplitSeq(content, []byte("\n")) {
					foundEventsByNS[ns] = append(foundEventsByNS[ns], e)
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
	// Metrics are covered in end-to-end tests, as port forwarding is easier to set up in an actual cluster.
})

// areEqual checks if objects a and e are equal, by namespace, name, metadata (labels and annotations) and Spec or
// equivalent fields.
// Notably, status fields and any auto-computed fields, such as managed fields, UID, etc are not compared.
func areEqual(a, e client.Object) bool {
	if a.GetName() != e.GetName() {
		return false
	}

	if a.GetNamespace() != e.GetNamespace() {
		return false
	}

	if !maps.Equal(a.GetLabels(), e.GetLabels()) ||
		!maps.Equal(a.GetAnnotations(), e.GetAnnotations()) {
		return false
	}

	// GVK is not populated on the expected object
	switch a.GetObjectKind().GroupVersionKind().Kind {
	case "Bundle":
		return reflect.DeepEqual(a.(*fleet.Bundle).Spec, e.(*fleet.Bundle).Spec)
	case "BundleDeployment":
		return reflect.DeepEqual(a.(*fleet.BundleDeployment).Spec, e.(*fleet.BundleDeployment).Spec)
	case "BundleNamespaceMapping":
		aMapping := a.(*fleet.BundleNamespaceMapping)
		eMapping := e.(*fleet.BundleNamespaceMapping)
		return reflect.DeepEqual(aMapping.BundleSelector, eMapping.BundleSelector) &&
			reflect.DeepEqual(aMapping.NamespaceSelector, eMapping.NamespaceSelector)
	case "Cluster":
		return reflect.DeepEqual(a.(*fleet.Cluster).Spec, e.(*fleet.Cluster).Spec)
	case "ClusterGroup":
		return reflect.DeepEqual(a.(*fleet.ClusterGroup).Spec, e.(*fleet.ClusterGroup).Spec)
	case "GitRepo":
		return reflect.DeepEqual(a.(*fleet.GitRepo).Spec, e.(*fleet.GitRepo).Spec)
	case "GitRepoRestriction":
		aGR := a.(*fleet.GitRepoRestriction)
		eGR := e.(*fleet.GitRepoRestriction)
		return aGR.DefaultServiceAccount == eGR.DefaultServiceAccount &&
			slices.Equal(aGR.AllowedServiceAccounts, eGR.AllowedServiceAccounts) &&
			slices.Equal(aGR.AllowedRepoPatterns, eGR.AllowedRepoPatterns) &&
			aGR.DefaultClientSecretName == eGR.DefaultClientSecretName &&
			slices.Equal(aGR.AllowedClientSecretNames, eGR.AllowedClientSecretNames) &&
			slices.Equal(aGR.AllowedTargetNamespaces, eGR.AllowedTargetNamespaces)

	case "HelmOp":
		return reflect.DeepEqual(a.(*fleet.HelmOp).Spec, e.(*fleet.HelmOp).Spec)
	}

	return false
}

func mustCreateNS(ns string) {
	toCreate := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &toCreate))).NotTo(HaveOccurred())
}
