package bundlereader_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/bundlereader"
)

// newTarGz builds a tar.gz archive in memory containing the given files
// (map of relative name → content) and returns the bytes.
func newTarGz(files map[string][]byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, data := range files {
		_ = tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Size:     int64(len(data)),
			Mode:     0600,
		})
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

var _ = Describe("GetContent fetches HTTP sources", func() {
	var (
		srv          *httptest.Server
		archiveBytes []byte
	)

	BeforeEach(func() {
		archiveBytes = newTarGz(map[string][]byte{
			"README.md":          []byte("hello"),
			"subdir/config.yaml": []byte("key: value"),
		})
	})

	AfterEach(func() {
		if srv != nil {
			srv.Close()
			srv = nil
		}
	})

	When("the URL points to a tar.gz archive", func() {
		It("extracts the archive and returns the files", func() {
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(archiveBytes)
			}))

			files, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(), srv.URL+"/bundle.tar.gz", "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveKey("README.md"))
			Expect(files).To(HaveKey(filepath.Join("subdir", "config.yaml")))
		})
	})

	When("the URL has no recognisable extension but ?archive= is provided", func() {
		It("extracts using the override extension", func() {
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The server must NOT receive the ?archive= parameter.
				Expect(r.URL.Query().Get("archive")).To(BeEmpty())
				_, _ = w.Write(archiveBytes)
			}))

			files, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(), srv.URL+"/download?archive=tar.gz", "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveKey("README.md"))
		})
	})

	When("a correct ?checksum= is provided", func() {
		It("succeeds when the hash matches", func() {
			h := sha256.New()
			h.Write(archiveBytes)
			hexHash := hex.EncodeToString(h.Sum(nil))

			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The server must NOT receive the ?checksum= parameter.
				Expect(r.URL.Query().Get("checksum")).To(BeEmpty())
				_, _ = w.Write(archiveBytes)
			}))

			_, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(),
				srv.URL+"/bundle.tar.gz?checksum=sha256:"+hexHash, "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when the hash does not match", func() {
			wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(archiveBytes)
			}))

			_, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(),
				srv.URL+"/bundle.tar.gz?checksum=sha256:"+wrongHash, "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("checksum mismatch"))
		})
	})

	When("the URL points to a plain file", func() {
		It("writes the file under its URL base name", func() {
			content := []byte("plain content")
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(content)
			}))

			files, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(), srv.URL+"/data.txt", "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveKey("data.txt"))
			Expect(files["data.txt"]).To(Equal(content))
		})
	})
})

// newTarGzBomb returns a tar.gz whose single entry inflates to inflatedSize bytes.
// The entry is compressed with the default gzip settings so the archive payload is small.
func newTarGzBomb(inflatedSize int64) []byte {
	payload := bytes.Repeat([]byte("A"), int(inflatedSize))
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	Expect(tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "bomb.txt",
		Size:     inflatedSize,
		Mode:     0600,
	})).To(Succeed())
	_, err := tw.Write(payload)
	Expect(err).NotTo(HaveOccurred())
	Expect(tw.Close()).To(Succeed())
	Expect(gw.Close()).To(Succeed())
	return buf.Bytes()
}

// newZipBomb returns a zip archive whose single entry inflates to inflatedSize bytes.
func newZipBomb(inflatedSize int) []byte {
	payload := bytes.Repeat([]byte("A"), inflatedSize)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("bomb.txt")
	Expect(err).NotTo(HaveOccurred())
	_, err = w.Write(payload)
	Expect(err).NotTo(HaveOccurred())
	Expect(zw.Close()).To(Succeed())
	return buf.Bytes()
}

var _ = Describe("GetContent enforces extraction limits", func() {
	var srv *httptest.Server

	AfterEach(func() {
		if srv != nil {
			srv.Close()
			srv = nil
		}
	})

	When("a tar.gz archive inflates beyond the size limit", func() {
		It("returns a size-limit error", func() {
			// Patch the limit to something small so the test does not
			// write gigabytes to disk.
			orig := bundlereader.MaxDecompressedBytes
			bundlereader.MaxDecompressedBytes = 512
			DeferCleanup(func() { bundlereader.MaxDecompressedBytes = orig })

			bomb := newTarGzBomb(1024) // 1 KB inflated, limit set to 512 B
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(bomb)
			}))

			_, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(), srv.URL+"/bomb.tar.gz", "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("byte limit"))
		})
	})

	When("a zip archive inflates beyond the size limit", func() {
		It("returns a size-limit error", func() {
			orig := bundlereader.MaxDecompressedBytes
			bundlereader.MaxDecompressedBytes = 512
			DeferCleanup(func() { bundlereader.MaxDecompressedBytes = orig })

			bomb := newZipBomb(1024)
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(bomb)
			}))

			_, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(), srv.URL+"/bomb.zip", "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("byte limit"))
		})
	})

	When("an archive contains more files than the entry limit", func() {
		It("returns a file-count error", func() {
			orig := bundlereader.MaxArchiveFiles
			bundlereader.MaxArchiveFiles = 3
			DeferCleanup(func() { bundlereader.MaxArchiveFiles = orig })

			archive := newTarGz(map[string][]byte{
				"a.txt": []byte("1"), "b.txt": []byte("2"),
				"c.txt": []byte("3"), "d.txt": []byte("4"), "e.txt": []byte("5"),
			})
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(archive)
			}))

			_, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(), srv.URL+"/bundle.tar.gz", "",
				bundlereader.Auth{}, false, nil,
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("too many files"))
		})
	})
})
