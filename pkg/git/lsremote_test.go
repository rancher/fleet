package git

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

type MockRemoteLister struct {
	ReturnValues []*plumbing.Reference
	ReturnError  error
}

func (m *MockRemoteLister) List(o *gogit.ListOptions) ([]*plumbing.Reference, error) {
	return m.ReturnValues, m.ReturnError
}

var _ = Describe("git's getRevisionCommit tests", func() {
	var (
		mockFunc         func(string) gitRemoteLister
		originalFunction func(string) gitRemoteLister
	)

	JustBeforeEach(func() {
		originalFunction = newRemoteLister
		newRemoteLister = mockFunc
	})

	AfterEach(func() {
		newRemoteLister = originalFunction
	})

	Context("Passing a revision that is already a commit", func() {
		BeforeEach(func() {
			mockFunc = func(url string) gitRemoteLister {
				return &MockRemoteLister{}
			}
		})
		It("returns the revision", func() {
			g, error := newGit("test-url.com", &options{})
			Expect(error).ToNot(HaveOccurred())

			revision := "bdb35e1950b5829c88df134810a0aa9a7da9bc22"
			commit, error := g.getRevisionCommit(revision)
			Expect(error).ToNot(HaveOccurred())
			Expect(commit).To(Equal(revision))
		})
	})

	Context("When a revision is not found", func() {
		BeforeEach(func() {
			mockFunc = func(url string) gitRemoteLister {
				return &MockRemoteLister{}
			}
		})

		It("returns an error", func() {
			g, error := newGit("test-url.com", &options{})
			Expect(error).ToNot(HaveOccurred())

			revision := "v1.0.0"
			commit, error := g.getRevisionCommit(revision)
			Expect(commit).To(BeEmpty())
			Expect(error).To(HaveOccurred())
			Expect(error).To(Equal(ErrCommitNotFoundForRevision))
		})
	})

	Context("When the go-git List function fails", func() {
		BeforeEach(func() {
			mockFunc = func(_ string) gitRemoteLister {
				return &MockRemoteLister{
					ReturnError: fmt.Errorf("THIS IS A TEST ERROR"),
				}
			}
		})

		It("returns the go-git List error", func() {
			g, error := newGit("test-url.com", &options{})
			Expect(error).ToNot(HaveOccurred())

			revision := "v1.0.0"
			commit, error := g.getRevisionCommit(revision)
			Expect(commit).To(BeEmpty())
			Expect(error).To(HaveOccurred())
			Expect(error.Error()).To(Equal("THIS IS A TEST ERROR"))
		})
	})

	Context("When the given revision is a lighweight tag", func() {
		var tagCommit = "b2f7eacdeda55833e299efdd6955abb68f581547"
		BeforeEach(func() {
			mockFunc = func(_ string) gitRemoteLister {
				return &MockRemoteLister{
					ReturnValues: []*plumbing.Reference{
						plumbing.NewHashReference("HEAD", plumbing.NewHash("bdb35e1950b5829c88df134810a0aa9a7da9bc22")),
						plumbing.NewHashReference("refs/heads/main", plumbing.NewHash("bdb35e1950b5829c88df134810a0aa9a7da9bc22")),
						plumbing.NewHashReference("refs/tags/v0.0.1", plumbing.NewHash("b8a525671bfd03f32991b4ccec56517e74b3d7b0")),
						plumbing.NewHashReference("refs/tags/v0.0.1^{}", plumbing.NewHash("ffad72d77b26152ad74053d736cc1b96c82b7c99")),
						// this is the tag that it should return
						plumbing.NewHashReference("refs/tags/v1.0.0", plumbing.NewHash(tagCommit)),
					},
					ReturnError: nil,
				}
			}
		})

		It("returns the right commit", func() {
			g, error := newGit("test-url.com", &options{})
			Expect(error).ToNot(HaveOccurred())

			revision := "v1.0.0"
			commit, error := g.getRevisionCommit(revision)
			Expect(error).ToNot(HaveOccurred())
			Expect(commit).To(Equal(tagCommit))
		})

	})

	Context("When then given revision is an annotated tag", func() {
		var tagCommit = "b2f7eacdeda55833e299efdd6955abb68f581547"
		BeforeEach(func() {
			mockFunc = func(_ string) gitRemoteLister {
				return &MockRemoteLister{
					ReturnValues: []*plumbing.Reference{
						plumbing.NewHashReference("HEAD", plumbing.NewHash("bdb35e1950b5829c88df134810a0aa9a7da9bc22")),
						plumbing.NewHashReference("refs/heads/main", plumbing.NewHash("bdb35e1950b5829c88df134810a0aa9a7da9bc22")),
						plumbing.NewHashReference("refs/tags/v0.0.1", plumbing.NewHash("b8a525671bfd03f32991b4ccec56517e74b3d7b0")),
						// this is the reference that it should return
						plumbing.NewHashReference("refs/tags/v0.0.1^{}", plumbing.NewHash(tagCommit)),
						plumbing.NewHashReference("refs/tags/v1.0.0", plumbing.NewHash("ffad72d77b26152ad74053d736cc1b96c82b7c99")),
					},
					ReturnError: nil,
				}
			}
		})

		It("returns the commit that corresponds to the ^{} reference", func() {
			g, error := newGit("test-url.com", &options{})
			Expect(error).ToNot(HaveOccurred())

			revision := "v0.0.1"
			commit, error := g.getRevisionCommit(revision)
			Expect(error).ToNot(HaveOccurred())
			Expect(commit).To(Equal(tagCommit))
		})
	})
})
