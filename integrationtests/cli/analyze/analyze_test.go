package analyze

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/rancher/fleet/internal/cmd/cli"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Fleet analyze", func() {
	var (
		gitrepoName   string
		snapshotFile  string
		snapshot2File string
	)

	// Helper to run monitor and save snapshot
	runMonitor := func(ns string) string {
		cmd := cli.NewMonitor()
		args := []string{"--kubeconfig", kubeconfigPath, "-n", ns}
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(gbytes.NewBuffer())

		err := cmd.Execute()
		Expect(err).NotTo(HaveOccurred())

		return string(buf.Contents())
	}

	// Helper to run analyze command
	runAnalyze := func(args []string) (*gbytes.Buffer, *gbytes.Buffer, error) {
		cmd := cli.NewAnalyze()
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		errBuf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(errBuf)

		err := cmd.Execute()
		return buf, errBuf, err
	}

	BeforeEach(func() {
		namespace = "default"
		gitrepoName = "test-gitrepo"

		// Create temporary snapshot files
		var err error
		snapshotFile = filepath.Join(tmpdir, "snapshot1.json")
		snapshot2File = filepath.Join(tmpdir, "snapshot2.json")

		// Create a test gitrepo
		gitrepo := &fleet.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitrepoName,
				Namespace: namespace,
			},
			Spec: fleet.GitRepoSpec{
				Repo:   "https://github.com/rancher/fleet-examples",
				Branch: "master",
			},
		}
		err = k8sClient.Create(ctx, gitrepo)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, gitrepo)
			_ = os.Remove(snapshotFile)
			_ = os.Remove(snapshot2File)
		})
	})

	Context("with clean monitor output", func() {
		BeforeEach(func() {
			// Capture clean snapshot
			snapshot := runMonitor(namespace)
			err := os.WriteFile(snapshotFile, []byte(snapshot), 0644)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should output summary by default", func() {
			buf, _, err := runAnalyze([]string{snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("FLEET MONITORING SUMMARY"))
			Expect(output).To(ContainSubstring("RESOURCE COUNTS"))
			Expect(output).To(ContainSubstring("DIAGNOSTICS SUMMARY"))
			Expect(output).To(ContainSubstring("GitRepos:"))
		})

		It("should parse and display summary correctly", func() {
			buf, _, err := runAnalyze([]string{snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			// Should show at least 1 gitrepo (the one we created)
			Expect(output).To(MatchRegexp(`GitRepos:\s+\d+`))
		})

		It("should output JSON format", func() {
			buf, _, err := runAnalyze([]string{"--json", snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			var result struct {
				SnapshotCount int           `json:"snapshotCount"`
				Latest        *cli.Snapshot `json:"latest"`
			}
			err = json.Unmarshal(buf.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.SnapshotCount).To(Equal(1))
			Expect(result.Latest).NotTo(BeNil())
			Expect(result.Latest.GitRepos).To(HaveLen(1))
		})

		It("should show issues mode output", func() {
			buf, _, err := runAnalyze([]string{"--issues", snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("ISSUES DETECTED"))
			// Output should either show "No issues detected" or list specific issues
			Expect(output).To(Or(
				ContainSubstring("No issues detected"),
				ContainSubstring("✗"),
				ContainSubstring("⚠"),
			))
		})

		It("should show detailed output", func() {
			buf, _, err := runAnalyze([]string{"--detailed", snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("FLEET MONITORING SUMMARY"))
			Expect(output).To(ContainSubstring("ISSUES DETECTED"))
			Expect(output).To(ContainSubstring("API CONSISTENCY"))
		})
	})

	Context("with issues in monitor output", func() {
		var bundleName string
		var bdName string

		BeforeEach(func() {
			// Create a bundle with generation mismatch
			bundleName = "test-bundle-mismatch"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:       bundleName,
					Namespace:  namespace,
					Generation: 5,
				},
				Spec: fleet.BundleSpec{
					Paused: true,
				},
			}
			err := k8sClient.Create(ctx, bundle)
			Expect(err).NotTo(HaveOccurred())

			// Set observed generation to create mismatch
			bundle.Status.ObservedGeneration = 3
			err = k8sClient.Status().Update(ctx, bundle)
			Expect(err).NotTo(HaveOccurred())

			// Create a stuck bundledeployment
			bdName = "test-bd-stuck"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bdName,
					Namespace: namespace,
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-newcontent:options",
				},
			}
			err = k8sClient.Create(ctx, bd)
			Expect(err).NotTo(HaveOccurred())

			// Set different applied deployment ID
			bd.Status.AppliedDeploymentID = "s-oldcontent:options"
			err = k8sClient.Status().Update(ctx, bd)
			Expect(err).NotTo(HaveOccurred())

			// Capture snapshot with issues
			snapshot := runMonitor(namespace)
			err = os.WriteFile(snapshotFile, []byte(snapshot), 0644)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bundle)
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should detect and display issues", func() {
			buf, _, err := runAnalyze([]string{"--issues", snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("ISSUES DETECTED"))

			// Should detect stuck bundledeployment
			Expect(output).To(ContainSubstring("Stuck BundleDeployments"))
			Expect(output).To(ContainSubstring(bdName))

			// Should detect generation mismatch
			Expect(output).To(ContainSubstring("Generation Mismatch"))
			Expect(output).To(ContainSubstring(bundleName))
		})

		It("should show issues in summary mode", func() {
			buf, _, err := runAnalyze([]string{snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("DIAGNOSTICS SUMMARY"))
			// Should show non-zero counts for issues
			Expect(output).To(MatchRegexp(`Stuck BundleDeployments:\s+\d+`))
			Expect(output).To(MatchRegexp(`Generation Mismatches:\s+\d+`))
		})

		It("should include issues in JSON output", func() {
			buf, _, err := runAnalyze([]string{"--json", snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			var result struct {
				Latest *cli.Snapshot `json:"latest"`
			}
			err = json.Unmarshal(buf.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Latest.Diagnostics).NotTo(BeNil())
			Expect(result.Latest.Diagnostics.StuckBundleDeployments).NotTo(BeEmpty())
			Expect(result.Latest.Diagnostics.BundlesWithGenerationMismatch).NotTo(BeEmpty())
		})
	})

	Context("with multiple snapshots", func() {
		BeforeEach(func() {
			// Capture first snapshot (clean state)
			snapshot1 := runMonitor(namespace)
			err := os.WriteFile(snapshotFile, []byte(snapshot1), 0644)
			Expect(err).NotTo(HaveOccurred())

			// Create a bundle
			bundleName := "test-bundle-new"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleName,
					Namespace: namespace,
				},
				Spec: fleet.BundleSpec{
					Paused: true,
				},
			}
			err = k8sClient.Create(ctx, bundle)
			Expect(err).NotTo(HaveOccurred())

			// Capture second snapshot (with new bundle)
			snapshot2 := runMonitor(namespace)
			err = os.WriteFile(snapshot2File, []byte(snapshot2), 0644)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bundle)
			})
		})

		It("should compare two snapshots", func() {
			buf, _, err := runAnalyze([]string{"--compare", snapshot2File, snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("COMPARING SNAPSHOTS"))
			Expect(output).To(ContainSubstring("RESOURCE COUNTS"))
			// Should show change in bundles count
			Expect(output).To(ContainSubstring("Bundles:"))
		})

		It("should show diff with multi-snapshot file", func() {
			// Create a file with both snapshots
			snapshot1, err := os.ReadFile(snapshotFile)
			Expect(err).NotTo(HaveOccurred())
			snapshot2, err := os.ReadFile(snapshot2File)
			Expect(err).NotTo(HaveOccurred())

			multiFile := filepath.Join(tmpdir, "multi-snapshot.json")
			combined := string(snapshot1) + string(snapshot2)
			err = os.WriteFile(multiFile, []byte(combined), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(multiFile)

			buf, _, err := runAnalyze([]string{"--diff", multiFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Changes Across"))
			Expect(output).To(ContainSubstring("Snapshot 1"))
			Expect(output).To(ContainSubstring("RESOURCE COUNTS"))
		})

		It("should show all snapshots with --all", func() {
			// Create a file with both snapshots
			snapshot1, err := os.ReadFile(snapshotFile)
			Expect(err).NotTo(HaveOccurred())
			snapshot2, err := os.ReadFile(snapshot2File)
			Expect(err).NotTo(HaveOccurred())

			multiFile := filepath.Join(tmpdir, "multi-snapshot.json")
			combined := string(snapshot1) + string(snapshot2)
			err = os.WriteFile(multiFile, []byte(combined), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(multiFile)

			buf, _, err := runAnalyze([]string{"--all", multiFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Analyzing 2 snapshots"))
			Expect(output).To(ContainSubstring("Snapshot 1/2"))
			Expect(output).To(ContainSubstring("Snapshot 2/2"))
		})
	})

	Context("with invalid input", func() {
		It("should fail gracefully with non-existent file", func() {
			_, _, err := runAnalyze([]string{"nonexistent.json"})
			Expect(err).To(HaveOccurred())
		})

		It("should fail gracefully with invalid JSON", func() {
			invalidFile := filepath.Join(tmpdir, "invalid.json")
			err := os.WriteFile(invalidFile, []byte("not valid json"), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(invalidFile)

			_, _, err = runAnalyze([]string{invalidFile})
			Expect(err).To(HaveOccurred())
		})

		It("should fail gracefully with empty file", func() {
			emptyFile := filepath.Join(tmpdir, "empty.json")
			err := os.WriteFile(emptyFile, []byte(""), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(emptyFile)

			_, _, err = runAnalyze([]string{emptyFile})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no snapshots found"))
		})

		It("should require two files for --compare", func() {
			_, _, err := runAnalyze([]string{"--compare", snapshot2File})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("requires two snapshot files"))
		})

		It("should require at least 2 snapshots for --diff", func() {
			// Create file with single snapshot
			snapshot := runMonitor(namespace)
			singleFile := filepath.Join(tmpdir, "single.json")
			err := os.WriteFile(singleFile, []byte(snapshot), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(singleFile)

			_, _, err = runAnalyze([]string{"--diff", singleFile})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("need at least 2 snapshots"))
		})
	})

	Context("with --no-color flag", func() {
		BeforeEach(func() {
			snapshot := runMonitor(namespace)
			err := os.WriteFile(snapshotFile, []byte(snapshot), 0644)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should disable colored output", func() {
			buf, _, err := runAnalyze([]string{"--no-color", snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			// Should not contain ANSI color codes
			Expect(output).NotTo(ContainSubstring("\033["))
		})
	})

	Context("with content size information", func() {
		BeforeEach(func() {
			// Create a bundle with size metadata (don't create actual large content)
			bundleName := "test-bundle-size"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleName,
					Namespace: namespace,
				},
				Spec: fleet.BundleSpec{
					Paused: true,
				},
			}
			err := k8sClient.Create(ctx, bundle)
			Expect(err).NotTo(HaveOccurred())

			bundle.Status.ResourcesSHA256Sum = "abc123"
			err = k8sClient.Status().Update(ctx, bundle)
			Expect(err).NotTo(HaveOccurred())

			// Create a small content resource
			content := &fleet.Content{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "s-abc123",
					Namespace: namespace,
				},
				Content: []byte("small content"), // Small content for testing
			}
			err = k8sClient.Create(ctx, content)
			Expect(err).NotTo(HaveOccurred())

			snapshot := runMonitor(namespace)
			err = os.WriteFile(snapshotFile, []byte(snapshot), 0644)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bundle)
				_ = k8sClient.Delete(ctx, content)
			})
		})

		It("should include bundle size information in output", func() {
			buf, _, err := runAnalyze([]string{snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			// Should show total bundle size
			Expect(output).To(ContainSubstring("Total Size:"))
		})

		It("should include a non-zero count of contents with 0 reference counts", func() {
			buf, _, err := runAnalyze([]string{snapshotFile})
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())

			// This works without the need to create additional contents, because Content resource `s-abc123` above is
			// not referenced, as no Fleet controller is running.
			Expect(output).To(ContainSubstring("Contents with 0 reference count"))
		})
	})
})
