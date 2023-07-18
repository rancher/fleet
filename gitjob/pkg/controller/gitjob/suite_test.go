package gitjob_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGitjob(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gitjob Suite")
}
