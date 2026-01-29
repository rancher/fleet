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
})
