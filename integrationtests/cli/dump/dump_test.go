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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	dumppkg "github.com/rancher/fleet/internal/cmd/cli/dump"
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

			testBundleDeployment1 := fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bundledeployment",
					Namespace: "ns1",
				},
			}

			testBundleDeployment2 := fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bundledeployment",
					Namespace: "ns2",
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
				&testBundleDeployment1,
				&testBundleDeployment2,
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
				Expect(fileName).To(HaveLen(3)) // kind, ns, name

				kindLow := fileName[0]

				switch kindLow {
				case "bundles":
					var b fleet.Bundle
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
				case "bundledeployments":
					var b fleet.BundleDeployment
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
				case "gitrepos":
					var b fleet.GitRepo
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
				case "helmops":
					var b fleet.HelmOp
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
				case "bundlenamespacemappings":
					var b fleet.BundleNamespaceMapping
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
				case "gitreporestrictions":
					var b fleet.GitRepoRestriction
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
				case "clusters":
					var b fleet.Cluster
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
				case "clustergroups":
					var b fleet.ClusterGroup
					err = yaml.Unmarshal(content, &b)
					Expect(err).ToNot(HaveOccurred())
					foundObjs = append(foundObjs, &b)
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
				Expect(found).To(BeTrue(), fmt.Sprintf("object %v not found", eo))
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

	When("the cluster contains secrets and the dump is requested with secrets", func() {
		It("includes secrets in the archive with full data", func() {
			tgzPath := "test_secrets.tgz"
			ns := "default"
			mustCreateNS(ns)

			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-secret",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"key": []byte("value"),
				},
			}

			Expect(k8sClient.Create(ctx, &secret)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &secret)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{WithSecrets: true}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, content, err := findFileInArchive(tgzPath, "secrets_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			var s corev1.Secret
			Expect(yaml.Unmarshal(content, &s)).ToNot(HaveOccurred())
			Expect(s.Name).To(Equal("my-secret"))
			Expect(s.Data).NotTo(BeEmpty())
			Expect(s.Data["key"]).To(Equal([]byte("value")))
		})
	})

	When("the cluster contains secrets and the dump is requested with secrets metadata only", func() {
		It("includes secrets in the archive without data", func() {
			tgzPath := "test_secrets_metadata.tgz"
			ns := "default"
			mustCreateNS(ns)

			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-secret",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"key": []byte("value"),
				},
			}

			Expect(k8sClient.Create(ctx, &secret)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &secret)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{WithSecretsMetadata: true}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, content, err := findFileInArchive(tgzPath, "secrets_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			var s corev1.Secret
			Expect(yaml.Unmarshal(content, &s)).ToNot(HaveOccurred())
			Expect(s.Name).To(Equal("my-secret"))
			Expect(s.Data).To(BeEmpty())
			Expect(s.StringData).To(BeEmpty())
		})
	})

	When("the cluster contains content and the dump is requested with content", func() {
		It("includes content in the archive with full data", func() {
			tgzPath := "test_contents.tgz"
			ns := "default"
			mustCreateNS(ns)

			content := fleet.Content{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-content",
					Namespace: ns,
				},
				Content:   []byte("test-content-data"),
				SHA256Sum: "abc123def456",
			}

			Expect(k8sClient.Create(ctx, &content)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &content)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{WithContent: true}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, fileContent, err := findFileInArchive(tgzPath, "contents_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			var c fleet.Content
			Expect(yaml.Unmarshal(fileContent, &c)).ToNot(HaveOccurred())
			Expect(c.Name).To(Equal("my-content"))
			Expect(c.Content).To(Equal([]byte("test-content-data")))
			Expect(c.SHA256Sum).To(Equal("abc123def456"))
		})
	})

	When("the cluster contains content and the dump is requested with content metadata only", func() {
		It("includes content in the archive without sensitive data", func() {
			tgzPath := "test_contents_metadata.tgz"
			ns := "default"
			mustCreateNS(ns)

			content := fleet.Content{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-content",
					Namespace: ns,
				},
				Content:   []byte("test-content-data"),
				SHA256Sum: "abc123def456",
			}

			Expect(k8sClient.Create(ctx, &content)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &content)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{WithContentMetadata: true}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, fileContent, err := findFileInArchive(tgzPath, "contents_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			var c fleet.Content
			Expect(yaml.Unmarshal(fileContent, &c)).ToNot(HaveOccurred())
			Expect(c.Name).To(Equal("my-content"))
			// Content field should be stripped in metadata-only mode
			Expect(c.Content).To(BeNil())
			// SHA256Sum and Status are metadata, should be preserved
			Expect(c.SHA256Sum).To(Equal("abc123def456"))
		})
	})

	When("the cluster contains secrets but no --with-secrets flag is provided", func() {
		It("excludes secrets from the archive", func() {
			tgzPath := "test_no_secrets.tgz"
			ns := "default"
			mustCreateNS(ns)

			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-secret",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"key": []byte("value"),
				},
			}

			Expect(k8sClient.Create(ctx, &secret)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &secret)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, _, err := findFileInArchive(tgzPath, "secrets_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeFalse())
		})
	})

	When("both --with-secrets and --with-secrets-metadata are set", func() {
		It("prefers full data over metadata-only", func() {
			tgzPath := "test_both_secrets.tgz"
			ns := "default"
			mustCreateNS(ns)

			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-secret",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"key": []byte("value"),
				},
			}

			Expect(k8sClient.Create(ctx, &secret)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &secret)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{WithSecrets: true, WithSecretsMetadata: true}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, content, err := findFileInArchive(tgzPath, "secrets_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			var s corev1.Secret
			Expect(yaml.Unmarshal(content, &s)).ToNot(HaveOccurred())
			Expect(s.Name).To(Equal("my-secret"))
			Expect(s.Data).NotTo(BeEmpty())
			Expect(s.Data["key"]).To(Equal([]byte("value")))
		})
	})

	When("the cluster contains content but no --with-content flag is provided", func() {
		It("excludes content from the archive", func() {
			tgzPath := "test_no_contents.tgz"
			ns := "default"
			mustCreateNS(ns)

			content := fleet.Content{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-content",
					Namespace: ns,
				},
			}

			Expect(k8sClient.Create(ctx, &content)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &content)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, _, err := findFileInArchive(tgzPath, "contents_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeFalse())
		})
	})

	When("both --with-content and --with-content-metadata are set", func() {
		It("prefers full data over metadata-only", func() {
			tgzPath := "test_both_contents.tgz"
			ns := "default"
			mustCreateNS(ns)

			content := fleet.Content{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-content",
					Namespace: ns,
				},
				Content:   []byte("test-content-data"),
				SHA256Sum: "abc123def456",
			}

			Expect(k8sClient.Create(ctx, &content)).NotTo(HaveOccurred())
			defer func() {
				Expect(k8sClient.Delete(ctx, &content)).NotTo(HaveOccurred())
			}()

			opts := dumppkg.Options{WithContent: true, WithContentMetadata: true}
			Expect(fleetDumpWithOptions(tgzPath, opts)).ToNot(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tgzPath)).ToNot(HaveOccurred())
			}()

			found, fileContent, err := findFileInArchive(tgzPath, "contents_")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			var c fleet.Content
			Expect(yaml.Unmarshal(fileContent, &c)).ToNot(HaveOccurred())
			Expect(c.Name).To(Equal("my-content"))
			Expect(c.Content).To(Equal([]byte("test-content-data")))
			Expect(c.SHA256Sum).To(Equal("abc123def456"))
		})
	})
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

// findFileInArchive searches for a file with the given prefix in the tar archive
// and returns true along with the file contents if found
func findFileInArchive(archivePath, filePrefix string) (bool, []byte, error) {
	f, err := os.OpenFile(archivePath, os.O_RDONLY, 0)
	if err != nil {
		return false, nil, err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return false, nil, err
	}

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false, nil, err
		}

		if strings.HasPrefix(header.Name, filePrefix) {
			content, err := io.ReadAll(tr)
			if err != nil {
				return false, nil, err
			}
			return true, content, nil
		}
	}

	return false, nil, nil
}
