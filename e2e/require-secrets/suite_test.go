package require_secrets

import (
	"os"
	"path"
	"testing"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(testenv.FailAndGather)
	RunSpecs(t, "E2E Suite for Github Secrets based Examples")
}

var (
	env            *testenv.Env
	khDir          string
	knownHostsPath string
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	testenv.SetRoot("../..")

	env = testenv.New()

	// setup SSH known_hosts for all tests, since environment variables are
	// shared between parallel test runs
	khDir, _ = os.MkdirTemp("", "fleet-")

	knownHostsPath = path.Join(khDir, "known_hosts")
	os.Setenv("SSH_KNOWN_HOSTS", knownHostsPath)
	out, err := githelper.CreateKnownHosts(knownHostsPath, os.Getenv("GIT_REPO_HOST"))
	Expect(err).ToNot(HaveOccurred(), out)
})

var _ = AfterSuite(func() {
	os.RemoveAll(khDir)
})
