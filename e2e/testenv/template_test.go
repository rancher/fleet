package testenv_test

import (
	"math/rand"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
)

var _ = Describe("Randomizing filenames", func() {
	var r *rand.Rand

	BeforeEach(func() {
		r = rand.New(rand.NewSource(1))
	})

	Context("with file extension", func() {
		When("it has a relative path", func() {
			It("adds a random number to the basename", func() {
				filename := testenv.RandomFilename("./foo/bar/gitrepo-template.yaml", r)
				Expect(filename).To(Equal("gitrepo-template11066.yaml"))
			})
		})
		When("it does not have a path", func() {
			It("adds a random number to the basename", func() {
				filename := testenv.RandomFilename("template.yaml", r)
				Expect(filename).To(Equal("template11066.yaml"))
			})
		})
		When("it has an absolute path", func() {
			It("adds a random number to the basename", func() {
				filename := testenv.RandomFilename("/foo/bar/gitrepo-template.yaml", r)
				Expect(filename).To(Equal("gitrepo-template11066.yaml"))
			})
		})
	})

	Context("without file extensions", func() {
		When("it has a relative path", func() {
			It("adds a random number to the basename", func() {
				filename := testenv.RandomFilename("./foo/bar/gitrepo-template", r)
				Expect(filename).To(Equal("gitrepo-template11066"))
			})
		})
		When("it does not have a path", func() {
			It("adds a random number to the basename", func() {
				filename := testenv.RandomFilename("template", r)
				Expect(filename).To(Equal("template11066"))
			})
		})
		When("it has an absolute path", func() {
			It("adds a random number to the basename", func() {
				filename := testenv.RandomFilename("/foo/bar/gitrepo-template", r)
				Expect(filename).To(Equal("gitrepo-template11066"))
			})
		})
	})
})

func TestTemplate(t *testing.T) {
	RegisterFailHandler(testenv.FailAndGather)
	RunSpecs(t, "Template Suite")
}
