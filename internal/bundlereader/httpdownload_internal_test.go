package bundlereader

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractTar_UnsupportedTypes verifies that extractTar returns an error
// for tar entry types that are not dir, regular file, or symlink. Hard links,
// device nodes, and FIFOs must be rejected to prevent unexpected side effects
// during archive extraction. Metadata-only entries (PAX headers, GNU long-name
// entries) are skipped rather than rejected, but Go's tar.Writer refuses to
// encode them, so that path can't be exercised via a synthetic archive here.
func TestExtractTar_UnsupportedTypes(t *testing.T) {
	rejected := []struct {
		name     string
		typeflag byte
	}{
		{"hard link", tar.TypeLink},
		{"char device", tar.TypeChar},
		{"block device", tar.TypeBlock},
		{"FIFO", tar.TypeFifo},
		{"contiguous file", tar.TypeCont},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			require.NoError(t, tw.WriteHeader(&tar.Header{
				Typeflag: tc.typeflag,
				Name:     "entry",
				Linkname: "target",
			}))
			require.NoError(t, tw.Close())

			err := extractTar(t.TempDir(), &buf)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported tar entry type")
		})
	}
}

// TestExtractTar_SymlinkTraversalRejected verifies that a symlink pointing
// outside the extraction root is rejected.
func TestExtractTar_SymlinkTraversalRejected(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink,
		Name:     "link",
		Linkname: "../../etc/passwd",
	}))
	require.NoError(t, tw.Close())

	err := extractTar(t.TempDir(), &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes destination directory")
}

// TestExtractZipFromReader verifies that extractZipFromReader correctly
// extracts a zip archive via the temp-file code path (not an in-memory buffer).
func TestExtractZipFromReader(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("hello.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("hello world"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	dst := t.TempDir()
	require.NoError(t, extractZipFromReader(dst, &buf))

	data, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(data))
}

// TestExtractResponse_Bzip2Dispatch verifies that extractResponse routes
// .tar.bz2, .tbz2, and .bz2 files to the bzip2 extraction path.
// Since compress/bzip2 is read-only in the stdlib (no writer), we confirm
// dispatch by checking that invalid bzip2 content produces a bzip2 error
// rather than an "unsupported" or other unrelated error.
func TestExtractResponse_Bzip2Dispatch(t *testing.T) {
	invalid := []byte("not bzip2")
	for _, ext := range []string{".tar.bz2", ".tbz2", ".bz2"} {
		t.Run(ext, func(t *testing.T) {
			err := extractResponse(t.TempDir(), "archive"+ext, "", bytes.NewReader(invalid))
			require.Error(t, err)
			// bzip2.NewReader returns an error on the first read when the magic
			// bytes are wrong; the message contains "bzip2".
			assert.Contains(t, err.Error(), "bzip2", "expected a bzip2 error for %s, got: %v", ext, err)
		})
	}
}

// TestExtractResponse_ZstdDispatch verifies that extractResponse routes
// .tar.zst, .tar.zstd, .tzst, .zst, and .zstd files to the zstd extraction path.
func TestExtractResponse_ZstdDispatch(t *testing.T) {
	invalid := []byte("not zstd")
	for _, ext := range []string{".tar.zst", ".tar.zstd", ".tzst", ".zst", ".zstd"} {
		t.Run(ext, func(t *testing.T) {
			err := extractResponse(t.TempDir(), "archive"+ext, "", bytes.NewReader(invalid))
			require.Error(t, err)
			// zstd.NewReader returns an error when the magic bytes are wrong.
			assert.Contains(t, err.Error(), "magic number mismatch", "expected a zstd error for %s, got: %v", ext, err)
		})
	}
}

// TestExtractResponse_XzDispatch verifies that extractResponse routes
// .tar.xz, .txz, and .xz files to the xz extraction path.
func TestExtractResponse_XzDispatch(t *testing.T) {
	invalid := []byte("not xz")
	for _, ext := range []string{".tar.xz", ".txz", ".xz"} {
		t.Run(ext, func(t *testing.T) {
			err := extractResponse(t.TempDir(), "archive"+ext, "", bytes.NewReader(invalid))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "xz", "expected an xz error for %s, got: %v", ext, err)
		})
	}
}

// TestParseChecksumParam covers valid and invalid ?checksum= inputs.
func TestParseChecksumParam(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"sha256 valid", "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", ""},
		{"sha1 valid", "sha1:aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d", ""},
		{"md5 valid", "md5:5d41402abc4b2a76b9719d911017c592", ""},
		{"sha512 valid", "sha512:9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043", ""},
		{"file variant", "file:http://example.com/sha.txt", "not supported"},
		{"no colon", "sha256deadbeef", "type:value format"},
		{"odd hex", "sha256:abc", "hex value"},
		{"unknown type", "md2:deadbeef", "unsupported hash type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseChecksumParam(tc.input)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

// TestHttpDownload_ChecksumVerification checks that a matching ?checksum=
// parameter succeeds and that the server does not receive the checksum param.
func TestHttpDownload_ChecksumVerification(t *testing.T) {
	body := []byte("hello world")
	h := sha256.New()
	h.Write(body)
	hexHash := hex.EncodeToString(h.Sum(nil))

	var receivedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dst := t.TempDir()
	err := httpDownload(t.Context(), dst, srv.URL+"/file.txt?checksum=sha256:"+hexHash+"&token=xyz", Auth{})
	require.NoError(t, err)

	assert.NotContains(t, receivedURL, "checksum", "?checksum= must not be forwarded to the server")
	assert.Contains(t, receivedURL, "token=xyz", "non-checksum query params must be preserved")

	data, err := os.ReadFile(filepath.Join(dst, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, body, data)
}

// TestHttpDownload_ChecksumMismatch verifies that a wrong ?checksum= value
// causes httpDownload to return a "checksum mismatch" error.
func TestHttpDownload_ChecksumMismatch(t *testing.T) {
	body := []byte("hello world")
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dst := t.TempDir()
	err := httpDownload(t.Context(), dst, srv.URL+"/file.txt?checksum=sha256:"+wrongHash, Auth{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

// TestHttpDownload_ArchiveOverride verifies that ?archive= forces format
// detection to use the given extension, allowing archives without a
// recognisable URL extension to be extracted correctly.
func TestHttpDownload_ArchiveOverride(t *testing.T) {
	// Build a minimal tar.gz in memory.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("override content")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "inner.txt",
		Size:     int64(len(content)),
		Mode:     0600,
	}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	archiveBytes := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBytes)
	}))
	defer srv.Close()

	dst := t.TempDir()
	// URL has no recognisable extension; ?archive=tar.gz triggers extraction.
	err = httpDownload(t.Context(), dst, srv.URL+"/download/bundle?archive=tar.gz", Auth{})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dst, "inner.txt"))
	require.NoError(t, err)
	assert.Equal(t, content, data)
}
