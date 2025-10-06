package git_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/pkg/git"
)

type FakeRemoteLister struct {
	RetValues []*git.RemoteRef
	RetError  error
}

func (f *FakeRemoteLister) List(appendPeeled bool) ([]*git.RemoteRef, error) {
	return f.RetValues, f.RetError
}

var _ = Describe("git's RevisionCommit tests", func() {
	var (
		gitRemote  *git.Remote
		fakeLister *FakeRemoteLister
	)

	JustBeforeEach(func() {
		gitRemote = &git.Remote{Lister: fakeLister}
	})

	Context("Passing a revision that is already a commit", func() {
		It("returns the revision", func() {
			revision := "bdb35e1950b5829c88df134810a0aa9a7da9bc22"
			commit, err := gitRemote.RevisionCommit(revision)
			Expect(err).ToNot(HaveOccurred())
			Expect(commit).To(Equal(revision))
		})
	})

	When("a revision is not found", func() {
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetValues: []*git.RemoteRef{
					{
						Name: "HEAD", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22",
					},
					{
						Name: "refs/heads/main", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22",
					},
				},
				RetError: nil,
			}
		})
		It("returns an error", func() {
			revision := "v1.0.0"
			commit, error := gitRemote.RevisionCommit(revision)
			Expect(commit).To(BeEmpty())
			Expect(error).To(HaveOccurred())
			Expect(error).To(Equal(fmt.Errorf("commit not found for revision: v1.0.0")))
		})
	})

	When("the go-git List function fails", func() {
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetError: fmt.Errorf("THIS IS A TEST ERROR"),
			}
		})

		It("returns the go-git List error", func() {
			revision := "v1.0.0"
			commit, error := gitRemote.RevisionCommit(revision)
			Expect(commit).To(BeEmpty())
			Expect(error).To(HaveOccurred())
			Expect(error.Error()).To(Equal("THIS IS A TEST ERROR"))
		})
	})

	When("the given revision is a lightweight tag", func() {
		var tagCommit = "b2f7eacdeda55833e299efdd6955abb68f581547"
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetValues: []*git.RemoteRef{
					{Name: "HEAD", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22"},
					{Name: "refs/heads/main", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22"},
					{Name: "refs/tags/v0.0.1", Hash: "b8a525671bfd03f32991b4ccec56517e74b3d7b0"},
					{Name: "refs/tags/v0.0.1^{}", Hash: "ffad72d77b26152ad74053d736cc1b96c82b7c99"},
					// this is the tag that it should return
					{Name: "refs/tags/v1.0.0", Hash: tagCommit},
				},
				RetError: nil,
			}
		})

		It("returns the right commit", func() {
			revision := "v1.0.0"
			commit, error := gitRemote.RevisionCommit(revision)
			Expect(error).ToNot(HaveOccurred())
			Expect(commit).To(Equal(tagCommit))
		})
	})

	When("then given revision is an annotated tag", func() {
		var tagCommit = "b2f7eacdeda55833e299efdd6955abb68f581547"
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetValues: []*git.RemoteRef{
					{Name: "HEAD", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22"},
					{Name: "refs/heads/main", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22"},
					{Name: "refs/tags/v0.0.1", Hash: "b8a525671bfd03f32991b4ccec56517e74b3d7b0"},
					{Name: "refs/tags/v0.0.1^{}", Hash: tagCommit},
					// this is the tag that it should return
					{Name: "refs/tags/v1.0.0", Hash: "ffad72d77b26152ad74053d736cc1b96c82b7c99"},
				},
				RetError: nil,
			}
		})

		It("returns the commit that corresponds to the ^{} reference", func() {
			revision := "v0.0.1"
			commit, error := gitRemote.RevisionCommit(revision)
			Expect(error).ToNot(HaveOccurred())
			Expect(commit).To(Equal(tagCommit))
		})
	})
})

var _ = Describe("git's LatestBranchCommit tests", func() {
	var (
		gitRemote  *git.Remote
		fakeLister *FakeRemoteLister
	)

	JustBeforeEach(func() {
		gitRemote = &git.Remote{Lister: fakeLister}
	})

	Context("Looking for a non existing branch", func() {
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetValues: []*git.RemoteRef{
					{
						Name: "HEAD", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22",
					},
					{
						Name: "refs/heads/main", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22",
					},
				},
				RetError: nil,
			}
		})
		It("returns an error", func() {
			commit, err := gitRemote.LatestBranchCommit("wrong_branch")
			Expect(err).To(HaveOccurred())
			Expect(commit).To(BeEmpty())
			Expect(err).To(Equal(fmt.Errorf("commit not found for branch: wrong_branch")))
		})
	})

	Context("Looking for branch that exists", func() {
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetValues: []*git.RemoteRef{
					{
						Name: "HEAD", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22",
					},
					{
						Name: "refs/heads/main", Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22",
					},
					{
						Name: "refs/heads/testbranch", Hash: "ffad72d77b26152ad74053d736cc1b96c82b7c99",
					},
				},
				RetError: nil,
			}
		})
		It("returns the expected commit", func() {
			commit, err := gitRemote.LatestBranchCommit("testbranch")
			Expect(err).ToNot(HaveOccurred())
			Expect(commit).To(Equal("ffad72d77b26152ad74053d736cc1b96c82b7c99"))
		})
	})

	When("the go-git List function fails", func() {
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetError: fmt.Errorf("THIS IS A TEST ERROR"),
			}
		})

		It("returns the go-git List error", func() {
			commit, error := gitRemote.LatestBranchCommit("main")
			Expect(commit).To(BeEmpty())
			Expect(error).To(HaveOccurred())
			Expect(error.Error()).To(Equal("THIS IS A TEST ERROR"))
		})
	})

	When("passing a non-valid branch", func() {
		BeforeEach(func() {
			fakeLister = &FakeRemoteLister{
				RetError: nil,
			}
		})
		It("LatestBranchCommitIfChanged returns an error", func() {
			commit, err := gitRemote.LatestBranchCommit("master.lock")
			Expect(err).To(HaveOccurred())
			Expect(commit).To(BeEmpty())
			Expect(err.Error()).To(Equal("invalid branch name: cannot end with \".lock\""))
		})
	})
})
