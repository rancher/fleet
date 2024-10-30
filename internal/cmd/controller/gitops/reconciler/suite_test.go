package reconciler

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGitOpsReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GitOps Reconciler Suite")
}

var _ = BeforeSuite(func() {
})
