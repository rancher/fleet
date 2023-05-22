package require_secrets

import (
	"log"
	"os"
	"testing"

	"github.com/rancher/fleet/e2e/testenv"

	"github.com/joho/godotenv"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite for Github Secrets based Examples")
}

var (
	env   *testenv.Env
	khDir string
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	testenv.SetRoot("../..")

	if err := godotenv.Load("../../.envrc"); err != nil {
		// Not fatal, as env variables may have been exported manually
		log.Println("could not load env file")
	}

	env = testenv.New()
})

var _ = AfterSuite(func() {
	os.RemoveAll(khDir)
})
