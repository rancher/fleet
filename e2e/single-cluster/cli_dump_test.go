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
	"github.com/rancher/fleet/internal/cmd/cli/dump"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Fleet dump", Label("sharding"), func() {
	When("the cluster has Fleet installed with metrics enabled", func() {
		It("includes metrics into the archive", func() {
			tgzPath := "test.tgz"

			err := dump.Create(context.Background(), restConfig, tgzPath)
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
				Expect(fileName).To(HaveLen(2))

				kindLow := fileName[0]
				if kindLow != "metrics" {
					continue
				}

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
					// Running this check on sharded services may fail, if no reconciles have run on sharded
					// controllers. Let's not introduce a dependency on other test cases here.
					// Same remark about gitjob services, for as long as no GitRepo has been created.
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

func mustCreateNS(ns string) {
	toCreate := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	Expect(client.IgnoreAlreadyExists(clientUpstream.Create(context.Background(), &toCreate))).NotTo(HaveOccurred())
}
