package bundlereader

import (
	"bytes"
	"context"
	"crypto/md5"  //nolint:gosec // md5 is only used as a data-integrity check per user-supplied ?checksum=, not for security
	"crypto/sha1" //nolint:gosec // sha1 is only used as a data-integrity check per user-supplied ?checksum=, not for security
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// httpDownload downloads src to dst.
//
// Supported archive extensions (extracted into dst as a directory):
//
//	.tar.gz / .tgz, .tar.bz2 / .tbz2, .tar.zst / .tzst, .tar.xz / .txz, .tar, .gz, .bz2, .zst, .xz, .zip
//
// A plain URL with no recognised extension is written as a single file.
//
// go-getter query parameters handled:
//
//	?checksum=<type>:<hex>  verify the response body against the given hash
//	                        (md5, sha1, sha256, sha512). The "file:" variant
//	                        (hash fetched from a URL) is not supported; provide
//	                        an inline hash instead.
//	?archive=<ext>          override format detection (e.g. "tar.gz" when the
//	                        URL path has no recognisable extension).
//
// Unrecognised query parameters are forwarded to the server unchanged.
func httpDownload(ctx context.Context, dst, src string, auth Auth) error {
	u, err := url.Parse(src)
	if err != nil {
		return fmt.Errorf("parsing URL %q: %w", redactURL(src), err)
	}

	q := u.Query()

	// Parse and strip ?checksum= before sending the request.
	var checksum *checksumSpec
	if cv := q.Get("checksum"); cv != "" {
		checksum, err = parseChecksumParam(cv)
		if err != nil {
			return fmt.Errorf("invalid ?checksum= in %q: %w", u.Redacted(), err)
		}
		q.Del("checksum")
	}

	// Parse and strip ?archive= before sending the request.
	archiveOverride := ""
	if av := q.Get("archive"); av != "" {
		if !strings.HasPrefix(av, ".") {
			av = "." + av
		}
		archiveOverride = strings.ToLower(av)
		q.Del("archive")
	}

	u.RawQuery = q.Encode()
	src = u.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return fmt.Errorf("building request for %q: %w", u.Redacted(), err)
	}
	if auth.Username != "" && auth.Password != "" {
		req.SetBasicAuth(auth.Username, auth.Password)
	}

	resp, err := getHTTPClient(auth).Do(req)
	if err != nil {
		return fmt.Errorf("downloading %q: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %q: unexpected status %d", u.Redacted(), resp.StatusCode)
	}

	if err := os.MkdirAll(dst, 0750); err != nil {
		return err
	}

	body := io.Reader(resp.Body)
	if checksum != nil {
		checksum.hash.Reset()
		body = io.TeeReader(resp.Body, checksum.hash)
	}
	// Limit the total compressed bytes read from the server before extracting.
	// This covers all archive formats uniformly; without it a streaming format
	// like tar.gz could pipe an unbounded compressed stream through the
	// decompressor until MaxDecompressedBytes fires, consuming CPU and I/O
	// with no bound on how much compressed data is transferred.
	lr := &downloadLimitReader{r: body, limit: MaxCompressedBytes}
	if err := extractResponse(dst, u.Path, archiveOverride, lr); err != nil {
		return err
	}

	if checksum != nil {
		actual := checksum.hash.Sum(nil)
		if !bytes.Equal(actual, checksum.expected) {
			return fmt.Errorf("checksum mismatch for %q: expected %s:%x, got %x",
				u.Redacted(), checksum.hashType, checksum.expected, actual)
		}
	}
	return nil
}

// MaxCompressedBytes caps the total number of compressed bytes accepted from
// a single download. It applies uniformly to all archive formats and prevents
// an arbitrarily large server response from consuming CPU and disk I/O before
// the total per-archive decompressed limit (MaxDecompressedBytes) enforced
// during extraction has a chance to fire.
var MaxCompressedBytes int64 = 2 * 1024 * 1024 * 1024

// downloadLimitReader wraps an io.Reader and returns an error once more than
// limit bytes have been read. Unlike io.LimitReader, it surfaces the overrun
// as an explicit error rather than a silent EOF, so callers receive a clear
// message instead of a confusing "unexpected EOF" from an archive parser.
type downloadLimitReader struct {
	r     io.Reader
	read  int64
	limit int64
}

func (d *downloadLimitReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	d.read += int64(n)
	if d.read > d.limit {
		return n, fmt.Errorf("download exceeds the %d byte limit", d.limit)
	}
	return n, err
}

type checksumSpec struct {
	hashType string
	hash     hash.Hash
	expected []byte
}

// parseChecksumParam parses a ?checksum=<type>:<hex> value.
//
// Supported hash types: md5, sha1, sha256, sha512.
// The "file:" variant (hash fetched from a remote URL) is not supported;
// that would require a recursive HTTP fetch and credential forwarding with
// no meaningful security benefit over an inline hash.
func parseChecksumParam(v string) (*checksumSpec, error) {
	typePart, valuePart, ok := strings.Cut(v, ":")
	if !ok {
		return nil, fmt.Errorf("must be in type:value format (e.g. sha256:<hex>), got %q", v)
	}
	if strings.EqualFold(typePart, "file") {
		return nil, errors.New(`the "file:" checksum variant is not supported; provide an inline hash (e.g. sha256:<hex>)`)
	}
	expected, err := hex.DecodeString(valuePart)
	if err != nil {
		return nil, fmt.Errorf("hex value: %w", err)
	}
	spec := &checksumSpec{hashType: strings.ToLower(typePart), expected: expected}
	switch spec.hashType {
	case "md5":
		spec.hash = md5.New() //nolint:gosec
	case "sha1":
		spec.hash = sha1.New() //nolint:gosec
	case "sha256":
		spec.hash = sha256.New()
	case "sha512":
		spec.hash = sha512.New()
	default:
		return nil, fmt.Errorf("unsupported hash type %q; use md5, sha1, sha256, or sha512", typePart)
	}
	return spec, nil
}
