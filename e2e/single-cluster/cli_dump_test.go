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

			// Create HelmOp in fleet-local namespace
			err = testenv.CreateHelmOp(k.Namespace(targetNs), targetNs, testName+"-helmop", "", "oci://ghcr.io/rancher/fleet-test-configmap-chart", "0.1.0", "")
			Expect(err).ToNot(HaveOccurred())

			// Create HelmOp in fleet-other namespace
			err = testenv.CreateHelmOp(k.Namespace(otherNs), otherNs, otherTestName+"-helmop", "", "oci://ghcr.io/rancher/fleet-test-configmap-chart", "0.1.0", "")
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
			_, _ = k.Namespace(targetNs).Delete("helmop", testName+"-helmop")
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

			// Verify HelmOps are filtered by namespace
			Expect(dumpedResources["helmops"]).To(ContainElement(ContainSubstring(testName+"-helmop")),
				"Should include HelmOp from target namespace")
			Expect(dumpedResources["helmops"]).ToNot(ContainElement(ContainSubstring(otherTestName+"-helmop")),
				"Should NOT include HelmOp from other namespace")
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

			// Verify HelmOps from both namespaces are included
			Expect(dumpedResources["helmops"]).To(ContainElement(ContainSubstring(testName+"-helmop")),
				"Should include HelmOp from target namespace")
			Expect(dumpedResources["helmops"]).To(ContainElement(ContainSubstring(otherTestName+"-helmop")),
				"Should include HelmOp from other namespace")
		})
	})

	When("filtering by GitRepo", func() {
		var (
			testName1        = "test-gitrepo-filter-repo1"
			testName2        = "test-gitrepo-filter-repo2"
			testPath1        = "simple"
			testPath2        = "simple-chart"
			namespace        = "fleet-local"
			tgzPath          = "test-gitrepo-filter.tgz"
			repo1ContentName string
			repo2ContentName string
		)

		BeforeEach(func() {
			k := env.Kubectl

			// Create two GitRepos in the same namespace
			err := testenv.CreateGitRepo(k.Namespace(namespace), namespace, testName1, "master", "", testPath1)
			Expect(err).ToNot(HaveOccurred())

			err = testenv.CreateGitRepo(k.Namespace(namespace), namespace, testName2, "master", "", testPath2)
			Expect(err).ToNot(HaveOccurred())

			// Wait for bundles from both GitRepos
			Eventually(func() bool {
				out, err := k.Namespace(namespace).Get("bundles", "-l", "fleet.cattle.io/repo-name="+testName1)
				if err != nil {
					return false
				}
				return strings.Contains(out, testName1)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			Eventually(func() bool {
				out, err := k.Namespace(namespace).Get("bundles", "-l", "fleet.cattle.io/repo-name="+testName2)
				if err != nil {
					return false
				}
				return strings.Contains(out, testName2)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Get cluster namespace
			var clusterNs string
			Eventually(func() bool {
				out, err := k.Namespace(namespace).Run("get", "cluster", "local", "-o", "jsonpath={.status.namespace}")
				if err != nil || out == "" {
					return false
				}
				clusterNs = out
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Wait for BundleDeployments for both repos
			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Get("bundledeployments")
				if err != nil {
					return false
				}
				return strings.Contains(out, testName1) && strings.Contains(out, testName2)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Get content names for both repos
			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Run(
					"get", "bundledeployments",
					"-l", "fleet.cattle.io/bundle-namespace="+namespace,
					"-l", "fleet.cattle.io/bundle-name="+testName1+"-"+testPath1,
					"-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}",
				)
				if err != nil || out == "" {
					return false
				}
				repo1ContentName = strings.Fields(out)[0]
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Run(
					"get", "bundledeployments",
					"-l", "fleet.cattle.io/bundle-namespace="+namespace,
					"-l", "fleet.cattle.io/bundle-name="+testName2+"-"+testPath2,
					"-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}",
				)
				if err != nil || out == "" {
					return false
				}
				repo2ContentName = strings.Fields(out)[0]
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			GinkgoWriter.Printf("Repo1 content name: %s\n", repo1ContentName)
			GinkgoWriter.Printf("Repo2 content name: %s\n", repo2ContentName)
		})

		AfterEach(func() {
			k := env.Kubectl.Namespace(namespace)
			_, _ = k.Delete("gitrepo", testName1)
			_, _ = k.Delete("gitrepo", testName2)
			_ = os.RemoveAll(tgzPath)
		})

		It("dumps only resources from the specified GitRepo", func() {
			// Create dump filtered by GitRepo
			err := dump.Create(context.Background(), restConfig, tgzPath, dump.Options{
				Namespace:           namespace,
				GitRepo:             testName1,
				AllNamespaces:       false,
				WithContent:         true,
				WithSecretsMetadata: true,
			})
			Expect(err).ToNot(HaveOccurred())

			// Parse the archive and collect dumped resources
			dumpedResources := extractResourcesFromArchive(tgzPath)

			// Verify GitRepos - should only include testName1
			Expect(dumpedResources["gitrepos"]).To(ContainElement(ContainSubstring(testName1)),
				"Should include GitRepo "+testName1)
			Expect(dumpedResources["gitrepos"]).ToNot(ContainElement(ContainSubstring(testName2)),
				"Should NOT include GitRepo "+testName2)
			Expect(dumpedResources["gitrepos"]).To(HaveLen(1),
				"Should have exactly 1 GitRepo")

			// Verify Bundles - should only include testName1's bundles
			foundTestName1Bundle := false
			foundTestName2Bundle := false
			for _, bundle := range dumpedResources["bundles"] {
				if strings.Contains(bundle, testName1) {
					foundTestName1Bundle = true
				}
				if strings.Contains(bundle, testName2) {
					foundTestName2Bundle = true
				}
			}
			Expect(foundTestName1Bundle).To(BeTrue(), "Should include bundles from "+testName1)
			Expect(foundTestName2Bundle).To(BeFalse(), "Should NOT include bundles from "+testName2)

			// Verify BundleDeployments - should only include testName1's
			foundTestName1BD := false
			foundTestName2BD := false
			for _, bd := range dumpedResources["bundledeployments"] {
				if strings.Contains(bd, testName1) {
					foundTestName1BD = true
				}
				if strings.Contains(bd, testName2) {
					foundTestName2BD = true
				}
			}
			Expect(foundTestName1BD).To(BeTrue(), "Should include BundleDeployments from "+testName1)
			Expect(foundTestName2BD).To(BeFalse(), "Should NOT include BundleDeployments from "+testName2)

			// Verify Contents - should only include testName1's content
			foundTestName1Content := false
			foundTestName2Content := false
			for _, content := range dumpedResources["contents"] {
				if strings.Contains(content, repo1ContentName) {
					foundTestName1Content = true
				}
				if strings.Contains(content, repo2ContentName) {
					foundTestName2Content = true
				}
			}
			Expect(foundTestName1Content).To(BeTrue(), "Should include content from "+testName1)
			Expect(foundTestName2Content).To(BeFalse(), "Should NOT include content from "+testName2)

			// Verify Clusters are still included (they're namespace-scoped, not GitRepo-scoped)
			Expect(dumpedResources["clusters"]).To(Not(BeEmpty()), "Should include clusters from the namespace")
		})
	})

	When("filtering by Bundle", func() {
		var (
			testName1          = "test-bundle-filter-bundle1"
			testName2          = "test-bundle-filter-bundle2"
			testPath1          = "simple"
			testPath2          = "simple-chart"
			namespace          = "fleet-local"
			tgzPath            = "test-bundle-filter.tgz"
			bundle1ContentName string
			bundle2ContentName string
			bundleName1        string
			bundleName2        string
		)

		BeforeEach(func() {
			bundleName1 = testName1 + "-" + testPath1
			bundleName2 = testName2 + "-" + testPath2

			k := env.Kubectl.Namespace(namespace)

			// Create two separate GitRepos to get distinct bundles
			err := testenv.CreateGitRepo(k, namespace, testName1, "master", "", testPath1)
			Expect(err).ToNot(HaveOccurred())

			err = testenv.CreateGitRepo(k, namespace, testName2, "master", "", testPath2)
			Expect(err).ToNot(HaveOccurred())

			// Wait for both bundles to be created
			Eventually(func() bool {
				out, err := k.Get("bundles", "-l", "fleet.cattle.io/repo-name="+testName1, "-o", "jsonpath={.items[*].metadata.name}")
				return err == nil && strings.TrimSpace(out) != ""
			}).Should(BeTrue(), "Bundle for "+testName1+" should be created")

			Eventually(func() bool {
				out, err := k.Get("bundles", "-l", "fleet.cattle.io/repo-name="+testName2, "-o", "jsonpath={.items[*].metadata.name}")
				return err == nil && strings.TrimSpace(out) != ""
			}).Should(BeTrue(), "Bundle for "+testName2+" should be created")

			// Get cluster namespace
			var clusterNs string
			Eventually(func() bool {
				out, err := k.Namespace(namespace).Run("get", "cluster", "local", "-o", "jsonpath={.status.namespace}")
				if err != nil || out == "" {
					return false
				}
				clusterNs = out
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Get("bundledeployments", "-l", "fleet.cattle.io/bundle-name="+testName1+"-"+testPath1, "-o", "name")
				if err != nil {
					return false
				}
				return strings.Contains(out, testName1+"-"+testPath1)
			}).Should(BeTrue(), "BundleDeployment for "+testName1+" should be created")

			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Get("bundledeployments", "-l", "fleet.cattle.io/bundle-name="+testName2+"-"+testPath2, "-o", "name")
				if err != nil {
					return false
				}
				return strings.Contains(out, testName2+"-"+testPath2)
			}).Should(BeTrue(), "BundleDeployment for "+testName2+" should be created")

			// Get content names from bundledeployments
			out, err := k.Namespace(clusterNs).Get("bundledeployments", "-l", "fleet.cattle.io/bundle-name="+bundleName1, "-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}")
			Expect(err).ToNot(HaveOccurred())
			bundle1ContentName = strings.TrimSpace(out)
			GinkgoWriter.Printf("Bundle 1 content name: %s\n", bundle1ContentName)

			out, err = k.Namespace(clusterNs).Get("bundledeployments", "-l", "fleet.cattle.io/bundle-name="+bundleName2, "-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}")
			Expect(err).ToNot(HaveOccurred())
			bundle2ContentName = strings.TrimSpace(out)
			GinkgoWriter.Printf("Bundle 2 content name: %s\n", bundle2ContentName)
		})

		AfterEach(func() {
			// Clean up the GitRepos and dump file
			k := env.Kubectl.Namespace(namespace)
			_, _ = k.Delete("gitrepo", "test-bundle-filter-bundle1")
			_, _ = k.Delete("gitrepo", "test-bundle-filter-bundle2")
			_ = os.Remove(tgzPath)
		})

		It("includes only resources from the specified bundle", func() {
			// Run dump with Bundle filter
			err := dump.Create(context.Background(), restConfig, tgzPath, dump.Options{
				Namespace:   namespace,
				Bundle:      bundleName1, // Filter by first bundle only
				WithContent: true,
			})
			Expect(err).ToNot(HaveOccurred())

			// Extract and analyze the dump
			dumpedResources := extractResourcesFromArchive(tgzPath)

			// Verify bundles: should include testName1, not testName2
			Expect(dumpedResources["bundles"]).To(Not(BeEmpty()), "Should have bundles")
			foundTestName1Bundle := false
			foundTestName2Bundle := false
			for _, bundleFile := range dumpedResources["bundles"] {
				if strings.Contains(bundleFile, bundleName1) {
					foundTestName1Bundle = true
				}
				if strings.Contains(bundleFile, bundleName2) {
					foundTestName2Bundle = true
				}
			}
			Expect(foundTestName1Bundle).To(BeTrue(), "Should include bundle "+bundleName1)
			Expect(foundTestName2Bundle).To(BeFalse(), "Should NOT include bundle "+bundleName2)

			// Verify GitRepos: should NOT be included when filtering by Bundle
			Expect(dumpedResources["gitrepos"]).To(BeEmpty(), "Should NOT include gitrepos when filtering by Bundle")

			// Verify BundleDeployments: should only include those from testName1
			if bundledeployments, hasBDs := dumpedResources["bundledeployments"]; hasBDs && len(bundledeployments) > 0 {
				foundTestName1BD := false
				foundTestName2BD := false
				for _, bdFile := range bundledeployments {
					// BundleDeployments reference bundles via labels - check the file content would be more accurate
					// but file names often include identifiable info
					if strings.Contains(bdFile, bundleName1) {
						foundTestName1BD = true
					}
					if strings.Contains(bdFile, bundleName2) {
						foundTestName2BD = true
					}
				}
				if foundTestName1BD || foundTestName2BD {
					GinkgoWriter.Printf("BundleDeployments: testName1=%v, testName2=%v\n", foundTestName1BD, foundTestName2BD)
					Expect(foundTestName2BD).To(BeFalse(), "Should NOT include BundleDeployments from "+bundleName2)
				}
			}

			// Verify Contents: should only include content from bundleName1
			if contents, hasContents := dumpedResources["contents"]; hasContents && len(contents) > 0 {
				foundTestName1Content := false
				foundTestName2Content := false
				for _, contentFile := range contents {
					if bundle1ContentName != "" && strings.Contains(contentFile, bundle1ContentName) {
						foundTestName1Content = true
					}
					if bundle2ContentName != "" && strings.Contains(contentFile, bundle2ContentName) {
						foundTestName2Content = true
					}
				}
				Expect(foundTestName1Content).To(BeTrue(), "Should include content from "+bundleName1)
				Expect(foundTestName2Content).To(BeFalse(), "Should NOT include content from "+bundleName2)
			}

			// Verify Clusters are still included (they're namespace-scoped, not Bundle-scoped)
			Expect(dumpedResources["clusters"]).To(Not(BeEmpty()), "Should include clusters from the namespace")
		})
	})

	When("filtering by HelmOp", func() {
		var (
			testName1        = "test-helmop-filter-helm1"
			testName2        = "test-helmop-filter-helm2"
			namespace        = "fleet-local"
			tgzPath          = "test-helmop-filter.tgz"
			helm1ContentName string
			helm2ContentName string
		)

		BeforeEach(func() {
			k := env.Kubectl.Namespace(namespace)

			// Create two HelmOps with the same chart (different names for filtering test)
			err := testenv.CreateHelmOp(k, namespace, testName1, "", "oci://ghcr.io/rancher/fleet-test-configmap-chart", "0.1.0", "")
			Expect(err).ToNot(HaveOccurred())

			err = testenv.CreateHelmOp(k, namespace, testName2, "", "oci://ghcr.io/rancher/fleet-test-configmap-chart", "0.1.0", "")
			Expect(err).ToNot(HaveOccurred())

			// Wait for bundles from both HelmOps
			Eventually(func() bool {
				out, err := k.Get("bundles", "-l", "fleet.cattle.io/fleet-helm-name="+testName1)
				return err == nil && strings.Contains(out, testName1)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			Eventually(func() bool {
				out, err := k.Get("bundles", "-l", "fleet.cattle.io/fleet-helm-name="+testName2)
				return err == nil && strings.Contains(out, testName2)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Get cluster namespace
			var clusterNs string
			Eventually(func() bool {
				out, err := k.Run("get", "cluster", "local", "-o", "jsonpath={.status.namespace}")
				if err != nil || out == "" {
					return false
				}
				clusterNs = out
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Wait for BundleDeployments for both HelmOps
			Eventually(func() bool {
				out, err := k.Namespace(clusterNs).Get("bundledeployments")
				if err != nil {
					return false
				}
				return strings.Contains(out, testName1) && strings.Contains(out, testName2)
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			// Get content names for both HelmOps
			Eventually(func() bool {
				// Get bundles for helm1 to find its content
				bundlesOut, err := k.Get("bundles", "-l", "fleet.cattle.io/fleet-helm-name="+testName1, "-o", "jsonpath={.items[*].metadata.name}")
				if err != nil || bundlesOut == "" {
					return false
				}
				bundleNames := strings.Fields(bundlesOut)
				if len(bundleNames) == 0 {
					return false
				}

				// Get content name from BundleDeployment for first bundle of helm1
				out, err := k.Namespace(clusterNs).Run(
					"get", "bundledeployments",
					"-l", "fleet.cattle.io/bundle-name="+bundleNames[0],
					"-l", "fleet.cattle.io/bundle-namespace="+namespace,
					"-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}",
				)
				if err != nil || out == "" {
					return false
				}
				helm1ContentName = strings.Fields(out)[0]
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			Eventually(func() bool {
				// Get bundles for helm2 to find its content
				bundlesOut, err := k.Get("bundles", "-l", "fleet.cattle.io/fleet-helm-name="+testName2, "-o", "jsonpath={.items[*].metadata.name}")
				if err != nil || bundlesOut == "" {
					return false
				}
				bundleNames := strings.Fields(bundlesOut)
				if len(bundleNames) == 0 {
					return false
				}

				// Get content name from BundleDeployment for first bundle of helm2
				out, err := k.Namespace(clusterNs).Run(
					"get", "bundledeployments",
					"-l", "fleet.cattle.io/bundle-name="+bundleNames[0],
					"-l", "fleet.cattle.io/bundle-namespace="+namespace,
					"-o", "jsonpath={.items[0].metadata.labels['fleet\\.cattle\\.io/content-name']}",
				)
				if err != nil || out == "" {
					return false
				}
				helm2ContentName = strings.Fields(out)[0]
				return true
			}, 30*time.Second, 2*time.Second).Should(BeTrue())

			GinkgoWriter.Printf("HelmOp1 content name: %s\n", helm1ContentName)
			GinkgoWriter.Printf("HelmOp2 content name: %s\n", helm2ContentName)
		})

		AfterEach(func() {
			k := env.Kubectl.Namespace(namespace)
			_, _ = k.Delete("helmop", testName1)
			_, _ = k.Delete("helmop", testName2)
			_ = os.RemoveAll(tgzPath)
		})

		It("dumps only resources from the specified HelmOp", func() {
			// Create dump filtered by HelmOp
			err := dump.Create(context.Background(), restConfig, tgzPath, dump.Options{
				Namespace:           namespace,
				HelmOp:              testName1,
				AllNamespaces:       false,
				WithContent:         true,
				WithSecretsMetadata: true,
			})
			Expect(err).ToNot(HaveOccurred())

			// Parse and verify archive contents
			dumpedResources := extractResourcesFromArchive(tgzPath)

			// Verify HelmOps
			Expect(dumpedResources["helmops"]).To(ContainElement(ContainSubstring(testName1)),
				"Should include HelmOp "+testName1)
			Expect(dumpedResources["helmops"]).ToNot(ContainElement(ContainSubstring(testName2)),
				"Should NOT include HelmOp "+testName2)
			Expect(dumpedResources["helmops"]).To(HaveLen(1))

			// Verify Bundles
			foundHelm1Bundle := false
			foundHelm2Bundle := false
			for _, bundle := range dumpedResources["bundles"] {
				if strings.Contains(bundle, testName1) {
					foundHelm1Bundle = true
				}
				if strings.Contains(bundle, testName2) {
					foundHelm2Bundle = true
				}
			}
			Expect(foundHelm1Bundle).To(BeTrue(), "Should include bundles from "+testName1)
			Expect(foundHelm2Bundle).To(BeFalse(), "Should NOT include bundles from "+testName2)

			// Verify BundleDeployments
			if bundledeployments, hasBDs := dumpedResources["bundledeployments"]; hasBDs && len(bundledeployments) > 0 {
				foundHelm1BD := false
				foundHelm2BD := false
				for _, bd := range bundledeployments {
					if strings.Contains(bd, testName1) {
						foundHelm1BD = true
					}
					if strings.Contains(bd, testName2) {
						foundHelm2BD = true
					}
				}
				Expect(foundHelm1BD).To(BeTrue(), "Should include bundledeployments from "+testName1)
				Expect(foundHelm2BD).To(BeFalse(), "Should NOT include bundledeployments from "+testName2)
			}

			// Verify Contents - should only include helm1's content
			if contents, hasContents := dumpedResources["contents"]; hasContents && len(contents) > 0 {
				foundHelm1Content := false
				foundHelm2Content := false
				for _, content := range contents {
					if helm1ContentName != "" && strings.Contains(content, helm1ContentName) {
						foundHelm1Content = true
					}
					if helm2ContentName != "" && strings.Contains(content, helm2ContentName) {
						foundHelm2Content = true
					}
				}
				Expect(foundHelm1Content).To(BeTrue(), "Should include content from "+testName1)
				Expect(foundHelm2Content).To(BeFalse(), "Should NOT include content from "+testName2)
			}

			// Verify GitRepos: should NOT be included when filtering by HelmOp
			Expect(dumpedResources["gitrepos"]).To(BeEmpty(), "Should NOT include gitrepos when filtering by HelmOp")

			// Verify Clusters are still included
			Expect(dumpedResources["clusters"]).To(Not(BeEmpty()), "Should include clusters from the namespace")
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
