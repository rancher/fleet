package singlecluster_test

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"k8s.io/apimachinery/pkg/util/uuid"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Image Scan", Label("infra-setup"), func() {
	var (
		clonedir string
		k        kubectl.Command
		assetdir string
	)

	JustBeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)

		tmpdir := GinkgoT().TempDir()
		clonedir = path.Join(tmpdir, "clone")
		setupRepo(k, tmpdir, clonedir, testenv.AssetPath(assetdir))
	})

	AfterEach(func() {
		_, _ = k.Delete("gitrepo", "imagescan")
		_, _ = k.Delete("secret", "git-auth")
	})

	When("update docker reference in git via image scan", func() {
		BeforeEach(func() {
			assetdir = "imagescan/repo"
		})
		It("updates the docker reference", func() {
			By("checking the deployment exists")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("pods")
				return out
			}).Should(ContainSubstring("nginx-"))

			By("checking for the original docker reference")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(`image: public\.ecr\.aws\/nginx\/nginx:latest # {"\$imagescan": "test-scan:digest"}`))

			By("checking for the updated docker reference")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(`image: public\.ecr\.aws\/nginx\/nginx:[0-9][.0-9]*@sha256:[0-9a-f]{64} # {"\$imagescan": "test-scan:digest"}`))

		})
	})
})

var _ = Describe("Image Scan dynamic tests pushing to ttl.sh", Label("infra-setup"), func() {
	var (
		clonedir   string
		k          kubectl.Command
		gh         *githelper.Git
		repository *git.Repository
		assetdir   string
		tmpRepoDir string
		image      string
		imageTag   string
	)

	JustBeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		tmpdir := GinkgoT().TempDir()
		clonedir = path.Join(tmpdir, "clone")
		repository = setupRepo(k, tmpdir, clonedir, tmpRepoDir)
	})

	AfterEach(func() {
		_, _ = k.Delete("gitrepo", "imagescan")
		_, _ = k.Delete("secret", "git-auth")
	})
	When("deploying imagescan setup with pre-release images", func() {
		BeforeEach(func() {
			assetdir = "imagescan/pre-releases-ok"
			tmpRepoDir = GinkgoT().TempDir()
			image, imageTag = initRegistryWithImageAndTag("k8s.gcr.io/pause", "0.0.0-40")
			applyTemplateValues(assetdir, tmpRepoDir, image, imageTag)
		})
		It("updates the docker reference", func() {
			By("checking the deployment exists")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("pods")
				return out
			}).Should(ContainSubstring("pause-prerelease-"))

			By("checking the bundle has the original image tag")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(fmt.Sprintf(`image: %s # {"\$imagescan": "test-scan"}`, imageTag)))

			By("pushing a new tag to the registry and checking the new image tag is found in the bundle")
			newTag := "0.0.0-50"
			imageTag = tagAndPushImage("k8s.gcr.io/pause", image, newTag)
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(fmt.Sprintf(`image: %s # {"\$imagescan": "test-scan"}`, imageTag)))

			By("checking that the new tag is pushed in the git repository")
			err := gh.CheckoutRemote(repository, "imagescan")
			Expect(err).NotTo(HaveOccurred())
			basedir := filepath.Join(clonedir, "examples")
			b, err := os.ReadFile(filepath.Join(basedir, "deployment.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).Should(ContainSubstring(newTag))
		})
	})

	// this test didn't pass before adding fix for issue #2096
	When("deploy imagescan setup with pre-release images and * semver range", func() {
		BeforeEach(func() {
			assetdir = "imagescan/pre-releases-ignored"
			tmpRepoDir = GinkgoT().TempDir()
			image, imageTag = initRegistryWithImageAndTag("k8s.gcr.io/pause", "0.0.0-40")
			applyTemplateValues(assetdir, tmpRepoDir, image, imageTag)
		})
		It("updates the image reference", func() {
			By("checking the deployment exists")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("pods")
				return out
			}).Should(ContainSubstring("pause-prerelease-"))

			By("checking the bundle has the original image tag")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(fmt.Sprintf(`image: %s # {"\$imagescan": "test-scan"}`, imageTag)))

			By("pushing a new tag to the registry and checking the fleet controller does not crash")
			// store number of fleet controller restarts to compare later
			index, ok := getFleetControllerContainerIndexInPod(k, "fleet-controller")
			Expect(ok).To(BeTrue())
			fleetControllerInitialRestarts := getFleetControllerRestarts(k, index)
			newTag := "0.0.0-50"
			previousImageTag := imageTag
			imageTag = tagAndPushImage("k8s.gcr.io/pause", image, newTag)

			// the scan time interval is 5 seconds.
			// we check for 10 seconds so we're sure that the image has been scanned and the controller didn't crash
			// Checks for number of restarts and also to the status.ready property to be more robust
			Consistently(func() bool {
				indexNow, ok := getFleetControllerContainerIndexInPod(k, "fleet-controller")
				Expect(ok).To(BeTrue())
				restarts := getFleetControllerRestarts(k, indexNow)
				ready := getFleetControllerReady(k, indexNow)
				return (restarts == fleetControllerInitialRestarts) && ready
			}, 10*time.Second, 1*time.Second).Should(BeTrue())

			By("checking the bundle has the original image tag")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(fmt.Sprintf(`image: %s # {"\$imagescan": "test-scan"}`, previousImageTag)))
		})
	})
	AfterEach(func() {
		_, _ = k.Delete("gitrepo", "imagescan")
		_, _ = k.Delete("secret", "git-auth")
	})
})

func getFleetControllerRestarts(k kubectl.Command, index int) int {
	out, err := k.Namespace("cattle-fleet-system").Get("pods", "-l", "app=fleet-controller", "-l", "fleet.cattle.io/shard-id=",
		"--no-headers",
		"-o", fmt.Sprintf("custom-columns=RESTARTS:.status.containerStatuses[%d].restartCount", index))
	Expect(err).NotTo(HaveOccurred())
	out = strings.TrimSuffix(out, "\n")
	n, err := strconv.Atoi(out)
	Expect(err).NotTo(HaveOccurred())
	return n
}

func getFleetControllerReady(k kubectl.Command, index int) bool {
	out, err := k.Namespace("cattle-fleet-system").Get("pods", "-l", "app=fleet-controller", "-l", "fleet.cattle.io/shard-id=",
		"--no-headers",
		"-o", fmt.Sprintf("custom-columns=RESTARTS:.status.containerStatuses[%d].ready", index))
	Expect(err).NotTo(HaveOccurred())
	out = strings.TrimSuffix(out, "\n")
	boolValue, err := strconv.ParseBool(out)
	Expect(err).NotTo(HaveOccurred())
	return boolValue
}

func getFleetControllerContainerIndexInPod(k kubectl.Command, container string) (int, bool) {
	// the fleet controller pod runs 3 containers.
	// we need to know the index of the fleet-controller container inside the pod.
	// get all the container names, and return the index of the given container name
	out, err := k.Namespace("cattle-fleet-system").Get("pods", "-l", "app=fleet-controller", "-l", "fleet.cattle.io/shard-id=",
		"--no-headers", "-o", "custom-columns=RESTARTS:.status.containerStatuses[*].name")
	Expect(err).NotTo(HaveOccurred())
	out = strings.TrimSuffix(out, "\n")
	containers := strings.Split(out, ",")
	for i, n := range containers {
		if container == n {
			return i, true
		}
	}
	return -1, false
}

func setupRepo(k kubectl.Command, tmpdir, clonedir, repoDir string) *git.Repository {
	// Create git secret
	out, err := k.Create(
		"secret", "generic", "git-auth", "--type", "kubernetes.io/basic-auth",
		"--from-literal=username="+os.Getenv("GIT_HTTP_USER"),
		"--from-literal=password="+os.Getenv("GIT_HTTP_PASSWORD"),
	)
	Expect(err).ToNot(HaveOccurred(), out)

	addr, err := githelper.GetExternalRepoAddr(env, port, repoName)
	Expect(err).ToNot(HaveOccurred())
	gh := githelper.NewHTTP(addr)
	gh.Branch = "imagescan"

	repo, err := gh.Create(clonedir, repoDir, "examples")
	Expect(err).ToNot(HaveOccurred())

	// Build git repo URL reachable _within_ the cluster, for the GitRepo
	host, err := githelper.BuildGitHostname(env.Namespace)
	Expect(err).ToNot(HaveOccurred())

	inClusterRepoURL := gh.GetInClusterURL(host, port, repoName)

	gitrepo := path.Join(tmpdir, "gitrepo.yaml")
	err = testenv.Template(gitrepo, testenv.AssetPath("imagescan/imagescan.yaml"), struct {
		Repo   string
		Branch string
	}{
		inClusterRepoURL,
		gh.Branch,
	})
	Expect(err).ToNot(HaveOccurred())

	out, err = k.Apply("-f", gitrepo)
	Expect(err).ToNot(HaveOccurred(), out)
	return repo
}

func tagAndPushImage(baseImage, image, tag string) string {
	imageTag := fmt.Sprintf("%s:%s", image, tag)
	// tag the image and push it to ttl.sh
	cmd := exec.Command("docker", "tag", baseImage, imageTag)
	err := cmd.Run()
	Expect(err).ToNot(HaveOccurred())
	// push the image to ttl.sh
	cmd = exec.Command("docker", "push", imageTag)
	err = cmd.Run()
	Expect(err).ToNot(HaveOccurred())
	return imageTag
}

func initRegistryWithImageAndTag(baseImage string, tag string) (string, string) {
	Eventually(func() error {
		cmd := exec.Command("docker", "pull", baseImage)
		err := cmd.Run()

		return err
	}, 20*time.Second, 1*time.Second).Should(Succeed())

	// generate a new uuid for this test
	uuid := uuid.NewUUID()
	image := fmt.Sprintf("ttl.sh/%s-fleet-test", uuid)
	imageTag := tagAndPushImage(baseImage, image, tag)

	return image, imageTag
}

func applyTemplateValues(assetdir, tmpRepoDir, image, imageTag string) {
	in := filepath.Join(testenv.AssetPath(assetdir), "fleet.yaml")
	out := filepath.Join(tmpRepoDir, "fleet.yaml")
	err := testenv.Template(out, in, struct {
		Image string
	}{
		image,
	})
	Expect(err).ToNot(HaveOccurred())

	in = filepath.Join(testenv.AssetPath(assetdir), "deployment.yaml")
	out = filepath.Join(tmpRepoDir, "deployment.yaml")
	err = testenv.Template(out, in, struct {
		ImageWithTag string
	}{
		imageTag,
	})
	Expect(err).ToNot(HaveOccurred())
}
