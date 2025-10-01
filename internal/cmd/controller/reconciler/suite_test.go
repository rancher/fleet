package reconciler

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGitOpsReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Controller Suite")
}

var _ = BeforeSuite(func() {
})
