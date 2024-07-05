package git

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("git's vendor specific functions tests", func() {
	When("using invalid url", func() {
		It("returns en empty string", func() {
			resPath := getVendorCommitsURL("this-is-a-fake-site.com", "mybranch")
			Expect(resPath).To(BeEmpty())
		})
	})

	When("using not parseable url", func() {
		It("returns en empty string", func() {
			resPath := getVendorCommitsURL("\n{this-is-not-an-url}\n", "mybranch")
			Expect(resPath).To(BeEmpty())
		})
	})

	When("using github host and non-valid address", func() {
		It("returns en empty string", func() {
			resPath := getVendorCommitsURL("https://github.com/thisisnotok", "mybranch")
			Expect(resPath).To(BeEmpty())
		})
	})

	When("using github host and valid address", func() {
		It("returns en empty string", func() {
			resPath := getVendorCommitsURL("https://github.com/rancher/fleet", "mybranch")
			Expect(resPath).To(Equal("https://api.github.com/repos/rancher/fleet/commits/mybranch"))
		})
	})

	When("using rancher host and non-valid address", func() {
		It("returns en empty string", func() {
			resPath := getVendorCommitsURL("https://git.rancher.io", "mybranch")
			Expect(resPath).To(BeEmpty())
		})
	})

	When("using rancher host and valid address", func() {
		It("returns en empty string", func() {
			resPath := getVendorCommitsURL("https://git.rancher.io/repository", "mybranch")
			Expect(resPath).To(Equal("https://git.rancher.io/repos/repository/commits/mybranch"))
		})
	})

	When("requesting if lastSha has changed with a valid URL", func() {
		It("returns the expected latest commit", func() {
			latest := "bdb35e1950b5829c88df134810a0aa9a7da9bc22"
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, latest)
			}))
			commit, err := latestCommitFromCommitsURL(svr.URL, &options{})
			Expect(err).ToNot(HaveOccurred())
			Expect(commit).To(Equal(latest))
		})

		It("the function is sending the expected headers", func() {
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				accept, ok := r.Header["Accept"]
				Expect(ok).To(BeTrue())
				Expect(len(accept)).To(Equal(1))
				Expect(accept[0]).To(Equal("application/vnd.github.v3.sha"))
				w.WriteHeader(http.StatusNotModified)
			}))
			_, _ = latestCommitFromCommitsURL(svr.URL, &options{})
		})

		It("returns an error when the server timeouts", func() {
			clientTimeout := time.Second
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(clientTimeout + 1)
				w.WriteHeader(http.StatusGatewayTimeout)
			}))
			commit, err := latestCommitFromCommitsURL(svr.URL, &options{Timeout: clientTimeout})
			Expect(err).To(HaveOccurred())
			Expect(commit).To(BeEmpty())
		})

		It("returns no error when cannot get a valid client, and changed returned is true", func() {
			clientTimeout := time.Second
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusGatewayTimeout)
			}))
			caBundle := []byte(`-----BEGIN CERTIFICATE-----
SUPER FAKE CERT
-----END CERTIFICATE-----`)
			commit, err := latestCommitFromCommitsURL(svr.URL, &options{CABundle: caBundle, Timeout: clientTimeout})
			// no error and returns true, so the client is forced to run the List to get results
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("x509: malformed certificate"))
			Expect(commit).To(BeEmpty())
		})
	})

	When("requesting if lastSha has changed with a non valid URL", func() {
		It("returns true and no error when the url is not parseable", func() {
			commit, err := latestCommitFromCommitsURL("httpssss://blahblah   blaisnotanurl\n", &options{})
			// Returns no error and changed = true so the user is forced to run the List to get results
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("parse \"httpssss://blahblah   blaisnotanurl\\n\": net/url: invalid control character in URL"))
			Expect(commit).To(BeEmpty())
		})
	})
})
