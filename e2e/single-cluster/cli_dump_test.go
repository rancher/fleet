package singlecluster_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/internal/cmd/cli/dump"
)

var _ = Describe("Fleet dump", Label("sharding"), func() {
	When("the cluster has Fleet installed with metrics enabled", func() {
		var (
			testName = "test-cli-dump"
		)

		It("includes metrics into the archive", func() {
			k := env.Kubectl.Namespace(env.Namespace)

			// Create a GitRepo to ensure fleet metrics are populated
			err := testenv.CreateGitRepo(k, env.Namespace, testName, "master", "", "simple")
			Expect(err).ToNot(HaveOccurred())

			// Clean up the test GitRepo after the test
			DeferCleanup(func() {
				out, err := k.Delete("gitrepo", testName)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			// Wait for Bundle to be created and have its status updated
			// This ensures metrics are collected by the Bundle controller
			Eventually(func() bool {
				out, err := k.Namespace(env.Namespace).Get("bundles")
				if err != nil {
					return false
				}
				// Check if at least one bundle exists and has been processed
				return strings.Contains(out, testName)
			}).Should(BeTrue())

			tgzPath := "test.tgz"

			err = dump.Create(context.Background(), restConfig, tgzPath, dump.Options{})
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

			foundFiles := []string{}
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

				kindLow := fileName[0]
				if kindLow != "metrics" {
					continue
				}

				Expect(fileName).To(HaveLen(2))
				Expect(content).ToNot(BeEmpty())

				// Run a few basic checks on expected strings, checking full contents would be cumbersome
				c := string(content)
				Expect(c).To(ContainSubstring("controller_runtime_active_workers"))
				Expect(c).To(ContainSubstring("controller_runtime_max_concurrent_reconciles"))
				Expect(c).To(ContainSubstring("controller_runtime_reconcile_total"))

				exampleMonitoredRsc := "bundle"
				if strings.Contains(fileName[1], "gitjob") {
					exampleMonitoredRsc = "gitrepo"
				} else if !strings.Contains(fileName[1], "shard") {
					// Check for fleet_*_desired_ready metrics on non-sharded services
					Expect(c).To(ContainSubstring(fmt.Sprintf("fleet_%s_desired_ready", exampleMonitoredRsc)))
				}

				Expect(c).To(ContainSubstring(fmt.Sprintf(`workqueue_work_duration_seconds_bucket{controller="%s",name="%s",`, exampleMonitoredRsc, exampleMonitoredRsc)))

				foundFiles = append(foundFiles, header.Name)
			}

			Expect(foundFiles).To(HaveLen(8))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-gitjob"))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-gitjob-shard-shard0"))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-gitjob-shard-shard1"))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-gitjob-shard-shard2"))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-fleet-controller"))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-fleet-controller-shard-shard0"))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-fleet-controller-shard-shard1"))
			Expect(foundFiles).To(ContainElement("metrics_monitoring-fleet-controller-shard-shard2"))
		})
	})

	When("filtering by namespace", func() {
		var (
			testName          = "test-cli-dump-target-namespace"
			otherTestName     = "test-cli-dump-other-namespace"
			targetNs          = "fleet-local" // We need BundleDeployments created, hence use fleet-local
			otherNs           = "fleet-other"
			tgzPath           = "test-namespace-filter.tgz"
			targetContentName string
			otherContentName  string
		)

		BeforeEach(func() {
			k := env.Kubectl // Create fleet-other namespace and a Cluster resource in it
			out, err := k.Run("create", "namespace", otherNs)
			Expect(err).ToNot(HaveOccurred(), out)

			err = testenv.CreateCluster(k.Namespace(otherNs), otherNs, otherTestName+"-cluster", nil, nil)
			Expect(err).ToNot(HaveOccurred())

			// Create GitRepo in fleet-local namespace
			err = testenv.CreateGitRepo(k.Namespace(targetNs), targetNs, testName, "master", "", "simple")
			Expect(err).ToNot(HaveOccurred())

			// Create GitRepo in fleet-other namespace
			err = testenv.CreateGitRepo(k.Namespace(otherNs), otherNs, otherTestName, "master", "", "simple")
			Expect(err).ToNot(HaveOccurred())

			// Wait for bundle in fleet-local
			Eventually(func() bool {
				out, err := k.Namespace(targetNs).Get("bundles")
				if err != nil {
					return false
				}
				return strings.Contains(out, testName)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Wait for bundle in fleet-other
			Eventually(func() bool {
				out, err := k.Namespace(otherNs).Get("bundles")
				if err != nil {
					return false
				}
				return strings.Contains(out, otherTestName)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Wait for the fleet-other cluster to have a namespace assigned
			var clusterNs string
			Eventually(func() bool {
				out, err := k.Namespace(otherNs).Run("get", "cluster", otherTestName+"-cluster", "-o", "jsonpath={.status.namespace}")
				if err != nil || out == "" {
					return false
				}
				clusterNs = out
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Wait for BundleDeployments in the fleet-other cluster namespace
			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Get("bundledeployments")
				if err != nil {
					return false
				}
				return strings.Contains(out, otherTestName)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Find the cluster namespace for fleet-local (it's the "local" cluster)
			var targetClusterNs string
			Eventually(func() bool {
				out, err := k.Namespace(targetNs).Run("get", "cluster", "local", "-o", "jsonpath={.status.namespace}")
				if err != nil || out == "" {
					return false
				}
				targetClusterNs = out
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Wait for BundleDeployments in fleet-local's cluster namespace
			Eventually(func() bool {
				out, err := k.Namespace(targetClusterNs).Get("bundledeployments")
				if err != nil {
					return false
				}
				return strings.Contains(out, testName)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Get content name from target namespace BundleDeployments
			Eventually(func() bool {
				out, err := k.Namespace(targetClusterNs).Run(
					"get", "bundledeployments",
					"-l", "fleet.cattle.io/bundle-namespace="+targetNs,
					"-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}",
				)
				if err != nil || out == "" {
					return false
				}
				targetContentName = out
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Get content name from other namespace BundleDeployments
			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Run(
					"get", "bundledeployments",
					"-l", "fleet.cattle.io/bundle-namespace="+otherNs,
					"-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}",
				)
				if err != nil || out == "" {
					return false
				}
				otherContentName = out
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Verify both content resources exist in the cluster
			out, err = k.Run("get", "content", targetContentName)
			Expect(err).ToNot(HaveOccurred(), "Target namespace content should exist: "+out)
			out, err = k.Run("get", "content", otherContentName)
			Expect(err).ToNot(HaveOccurred(), "Other namespace content should exist: "+out)

			GinkgoWriter.Printf("Target content name: %s\n", targetContentName)
			GinkgoWriter.Printf("Other content name: %s\n", otherContentName)
		})

		AfterEach(func() {
			k := env.Kubectl

			_, _ = k.Delete("namespace", otherNs) // just delete the extra namespace (inclusive GitRepo)
			_, _ = k.Namespace(targetNs).Delete("gitrepo", testName)
			_ = os.RemoveAll(tgzPath)
		})

		It("dumps only resources from the specified namespace", func() {
			// Create dump filtered by target namespace
			err := dump.Create(context.Background(), restConfig, tgzPath, dump.Options{
				Namespace:           targetNs,
				AllNamespaces:       false,
				WithContent:         true,
				WithSecretsMetadata: true,
			})
			Expect(err).ToNot(HaveOccurred())

			// Parse the archive and collect dumped resources
			dumpedResources := extractResourcesFromArchive(tgzPath)

			// Verify GitRepos
			Expect(dumpedResources["gitrepos"]).To(ContainElement(ContainSubstring(testName)),
				"Should include GitRepo from target namespace")
			Expect(dumpedResources["gitrepos"]).ToNot(ContainElement(ContainSubstring(otherTestName)),
				"Should NOT include GitRepo from other namespace")

			// Verify Bundles
			Expect(dumpedResources["bundles"]).To(ContainElement(ContainSubstring(testName)),
				"Should include Bundles from target namespace")
			Expect(dumpedResources["bundles"]).ToNot(ContainElement(ContainSubstring(otherTestName)),
				"Should NOT include Bundles from other namespace")

			// Verify BundleDeployments are included (they're in cluster namespace but labeled with bundle-namespace)
			foundBundleDeployments := false
			for _, bd := range dumpedResources["bundledeployments"] {
				if strings.Contains(bd, testName) {
					foundBundleDeployments = true
					break
				}
			}
			Expect(foundBundleDeployments).To(BeTrue(),
				"Should include BundleDeployments related to bundles in target namespace")

			// Verify we don't have BundleDeployments from other namespace
			foundOtherBundleDeployments := false
			for _, bd := range dumpedResources["bundledeployments"] {
				if strings.Contains(bd, otherTestName) {
					foundOtherBundleDeployments = true
					break
				}
			}
			Expect(foundOtherBundleDeployments).To(BeFalse(),
				"Should NOT include BundleDeployments from other namespace")

			// Verify Content resources are properly filtered
			Expect(dumpedResources["contents"]).ToNot(BeEmpty(), "Should have content resources")

			// Check that target namespace content is included
			foundTargetContent := false
			for _, contentFile := range dumpedResources["contents"] {
				if strings.Contains(contentFile, targetContentName) {
					foundTargetContent = true
					break
				}
			}
			Expect(foundTargetContent).To(BeTrue(),
				"Should include content from target namespace: "+targetContentName)

			// Check that other namespace content is NOT included
			foundOtherContent := false
			for _, contentFile := range dumpedResources["contents"] {
				if strings.Contains(contentFile, otherContentName) {
					foundOtherContent = true
					break
				}
			}
			Expect(foundOtherContent).To(BeFalse(),
				"Should NOT include content from other namespace: "+otherContentName)

			GinkgoWriter.Printf("Found %d content resources from filtered namespace\n", len(dumpedResources["contents"]))

			// Verify Events are filtered (only from target namespace and system namespaces)
			if events, hasEvents := dumpedResources["events"]; hasEvents {
				GinkgoWriter.Printf("Found %d event files\n", len(events))
				for _, eventFile := range events {
					namespace := strings.TrimPrefix(eventFile, "events_")
					Expect(namespace).To(Or(
						Equal(targetNs),
						Equal("kube-system"),
						Equal("default"),
						Equal("cattle-fleet-system"),
						Equal("cattle-fleet-local-system"),
						HavePrefix("cluster-"),
					), "events should only be from target or system namespaces, got: "+namespace)
					// Ensure we don't have events from otherNs
					Expect(namespace).ToNot(Equal(otherNs), "Should NOT have events from other namespace")
				}
			}

			// Verify Secrets metadata are filtered (format: secrets_<namespace>_<name>)
			if secrets, hasSecrets := dumpedResources["secrets"]; hasSecrets {
				GinkgoWriter.Printf("Found %d secret files\n", len(secrets))
				for _, secretFile := range secrets {
					// Extract namespace from "secrets_<namespace>_<name>" format
					parts := strings.SplitN(strings.TrimPrefix(secretFile, "secrets_"), "_", 2)
					Expect(parts).To(HaveLen(2), "Secret file should have format secrets_<namespace>_<name>")
					namespace := parts[0]
					Expect(namespace).To(Or(
						Equal(targetNs),
						Equal("kube-system"),
						Equal("default"),
						Equal("cattle-fleet-system"),
						Equal("cattle-fleet-local-system"),
						HavePrefix("cluster-"),
					), "secrets should only be from target or system namespaces, got: "+namespace+" in file "+secretFile)
					// Ensure we don't have secrets from otherNs
					Expect(namespace).ToNot(Equal(otherNs), "Should NOT have secrets from other namespace")
				}
			}
		})

		It("dumps all resources when using --all-namespaces", func() {
			// Create dump with all-namespaces flag (-A)
			// This should capture resources from all namespaces without any filtering
			err := dump.Create(context.Background(), restConfig, tgzPath, dump.Options{
				AllNamespaces: true,
				WithContent:   true,
				WithSecrets:   true, // Test with full secrets in all-namespaces mode
			})
			Expect(err).ToNot(HaveOccurred())

			// Parse the archive and collect dumped resources
			dumpedResources := extractResourcesFromArchive(tgzPath)

			// Verify both GitRepos are included
			Expect(dumpedResources["gitrepos"]).To(ContainElement(ContainSubstring(testName)),
				"Should include GitRepo from target namespace")
			Expect(dumpedResources["gitrepos"]).To(ContainElement(ContainSubstring(otherTestName)),
				"Should include GitRepo from other namespace")

			// Verify both Bundles are included
			Expect(dumpedResources["bundles"]).To(ContainElement(ContainSubstring(testName)),
				"Should include Bundles from target namespace")
			Expect(dumpedResources["bundles"]).To(ContainElement(ContainSubstring(otherTestName)),
				"Should include Bundles from other namespace")

			// Verify BundleDeployments from both namespaces are included
			Expect(dumpedResources["bundledeployments"]).To(Not(BeEmpty()),
				"Should include BundleDeployments")
			foundTargetBD := false
			foundOtherBD := false
			for _, bd := range dumpedResources["bundledeployments"] {
				if strings.Contains(bd, testName) {
					foundTargetBD = true
				}
				if strings.Contains(bd, otherTestName) {
					foundOtherBD = true
				}
			}
			Expect(foundTargetBD).To(BeTrue(),
				"Should include BundleDeployments related to target namespace bundles")
			Expect(foundOtherBD).To(BeTrue(),
				"Should include BundleDeployments related to other namespace bundles")

			// Verify Clusters from both namespaces are included
			Expect(dumpedResources["clusters"]).To(Not(BeEmpty()),
				"Should include Clusters")
			foundOtherCluster := false
			for _, cluster := range dumpedResources["clusters"] {
				if strings.Contains(cluster, otherTestName+"-cluster") {
					foundOtherCluster = true
					break
				}
			}
			Expect(foundOtherCluster).To(BeTrue(),
				"Should include Cluster from other namespace")

			// Verify Content resources from both namespaces are included
			Expect(dumpedResources["contents"]).ToNot(BeEmpty(), "Should have content resources")

			// Check that target namespace content is included
			foundTargetContent := false
			for _, contentFile := range dumpedResources["contents"] {
				if strings.Contains(contentFile, targetContentName) {
					foundTargetContent = true
					break
				}
			}
			Expect(foundTargetContent).To(BeTrue(),
				"Should include content from target namespace: "+targetContentName)

			// Check that other namespace content is included
			foundOtherContent := false
			for _, contentFile := range dumpedResources["contents"] {
				if strings.Contains(contentFile, otherContentName) {
					foundOtherContent = true
					break
				}
			}
			Expect(foundOtherContent).To(BeTrue(),
				"Should include content from other namespace: "+otherContentName)

			GinkgoWriter.Printf("Found %d content resources from all namespaces\n", len(dumpedResources["contents"]))

			// Verify Events from both target and other namespace are included
			if events, hasEvents := dumpedResources["events"]; hasEvents {
				GinkgoWriter.Printf("Found %d event files from all namespaces\n", len(events))
				foundTargetNsEvents := false
				foundOtherNsEvents := false
				for _, eventFile := range events {
					// Event files are named "events_<namespace>"
					if eventFile == "events_"+targetNs {
						foundTargetNsEvents = true
					}
					if eventFile == "events_"+otherNs {
						foundOtherNsEvents = true
					}
				}
				// Events may not always be present, so just log what we found
				GinkgoWriter.Printf("Events found: targetNs=%v, otherNs=%v\n", foundTargetNsEvents, foundOtherNsEvents)
			}

			// Verify Secrets from both namespaces are included
			if secrets, hasSecrets := dumpedResources["secrets"]; hasSecrets {
				GinkgoWriter.Printf("Found %d secret files from all namespaces\n", len(secrets))
				foundTargetNsSecrets := false
				foundOtherNsSecrets := false
				for _, secretFile := range secrets {
					// Extract namespace from "secrets_<namespace>_<name>" format
					parts := strings.SplitN(strings.TrimPrefix(secretFile, "secrets_"), "_", 2)
					if len(parts) >= 2 {
						namespace := parts[0]
						if namespace == targetNs {
							foundTargetNsSecrets = true
						}
						if namespace == otherNs {
							foundOtherNsSecrets = true
						}
					}
				}
				Expect(foundTargetNsSecrets || foundOtherNsSecrets).To(BeTrue(),
					"Should have secrets from at least one of the namespaces")
			}
		})
	})
})

// extractResourcesFromArchive extracts resources from a dump archive and returns a map of resource types to file names
func extractResourcesFromArchive(archivePath string) map[string][]string {
	resources := make(map[string][]string)

	f, err := os.Open(archivePath)
	Expect(err).ToNot(HaveOccurred())
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	Expect(err).ToNot(HaveOccurred())

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		Expect(err).ToNot(HaveOccurred())

		// Extract resource type from filename (format: resourcetype_namespace_name)
		parts := strings.Split(header.Name, "_")
		if len(parts) > 0 {
			resourceType := parts[0]
			resources[resourceType] = append(resources[resourceType], header.Name)
		}
	}

	return resources
}
