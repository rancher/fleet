package bundlereader

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// extractResponse writes the content of r into dst.
// filename is used to detect the archive type by extension.
// archiveOverride, when non-empty, takes precedence over the filename extension.
func extractResponse(dst, filename, archiveOverride string, r io.Reader) error {
	lower := archiveOverride
	if lower == "" {
		lower = strings.ToLower(filename)
	}
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(dst, r)
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		return extractTarBz2(dst, r)
	case strings.HasSuffix(lower, ".tar.zst"), strings.HasSuffix(lower, ".tar.zstd"),
		strings.HasSuffix(lower, ".tzst"):
		return extractTarZst(dst, r)
	case strings.HasSuffix(lower, ".tar"):
		return extractTar(dst, r)
	case strings.HasSuffix(lower, ".gz"):
		return extractGz(dst, filepath.Base(filename), r)
	case strings.HasSuffix(lower, ".bz2"):
		return extractBz2(dst, filepath.Base(filename), r)
	case strings.HasSuffix(lower, ".zst"), strings.HasSuffix(lower, ".zstd"):
		return extractZst(dst, filepath.Base(filename), r)
	case strings.HasSuffix(lower, ".zip"):
		return extractZipFromReader(dst, r)
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		return extractTarXz(dst, r)
	case strings.HasSuffix(lower, ".xz"):
		return extractXz(dst, filepath.Base(filename), r)
	default:
		// Plain file: write it under its base name inside dst.
		// filepath.Base returns "/" for a trailing-slash URL and "." for an
		// empty string; both would be unsafe as file names.
		name := filepath.Base(filename)
		if name == "/" || name == "." || name == "" {
			name = "file"
		}
		out, err := os.Create(filepath.Join(dst, name))
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, r)
		return err
	}
}

// extractTarGz decompresses a gzip-compressed tar archive into dst.
func extractTarGz(dst string, r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("opening gzip reader: %w", err)
	}
	defer gz.Close()
	return extractTar(dst, gz)
}

// extractTarBz2 decompresses a bzip2-compressed tar archive into dst.
func extractTarBz2(dst string, r io.Reader) error {
	return extractTar(dst, bzip2.NewReader(r))
}

// extractTarZst decompresses a zstd-compressed tar archive into dst.
func extractTarZst(dst string, r io.Reader) error {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return fmt.Errorf("opening zstd reader: %w", err)
	}
	defer zr.Close()
	return extractTar(dst, zr)
}

// extractTarXz decompresses an xz-compressed tar archive into dst.
func extractTarXz(dst string, r io.Reader) error {
	xr, err := xz.NewReader(r)
	if err != nil {
		return fmt.Errorf("opening xz reader: %w", err)
	}
	return extractTar(dst, xr)
}

// extractGz decompresses a single gzip-compressed file into dst/name.
func extractGz(dst, name string, r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("opening gzip reader: %w", err)
	}
	defer gz.Close()
	return extractSingleFile(dst, strings.TrimSuffix(name, ".gz"), gz)
}

// extractBz2 decompresses a single bzip2-compressed file into dst/name.
func extractBz2(dst, name string, r io.Reader) error {
	return extractSingleFile(dst, strings.TrimSuffix(name, ".bz2"), bzip2.NewReader(r))
}

// extractZst decompresses a single zstd-compressed file into dst/name.
func extractZst(dst, name string, r io.Reader) error {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return fmt.Errorf("opening zstd reader: %w", err)
	}
	defer zr.Close()
	// Handle both .zst and .zstd extensions (e.g. when ?archive=.zstd is used).
	return extractSingleFile(dst, strings.TrimSuffix(strings.TrimSuffix(name, ".zstd"), ".zst"), zr)
}

// extractXz decompresses a single xz-compressed file into dst/name.
func extractXz(dst, name string, r io.Reader) error {
	xr, err := xz.NewReader(r)
	if err != nil {
		return fmt.Errorf("opening xz reader: %w", err)
	}
	return extractSingleFile(dst, strings.TrimSuffix(name, ".xz"), xr)
}

// extractSingleFile writes the content of r into a single file under dst named
// outName. An empty outName is replaced with "file".
func extractSingleFile(dst, outName string, r io.Reader) error {
	if outName == "" {
		outName = "file"
	}
	out, err := os.Create(filepath.Join(dst, outName))
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, r)
	return err
}

// extractTar extracts a tar archive into dst.
func extractTar(dst string, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		target, err := safeJoin(dst, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode())
			if err != nil {
				return err
			}
			//nolint:gosec // G110: archive content is sourced from a trusted server configured by the cluster admin
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// Reject absolute symlink targets outright; they bypass path-safety checks.
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("symlink %q: absolute target not allowed in archive", hdr.Name)
			}
			// Validate the target resolves within the extraction root when expanded
			// relative to the directory containing the link (not relative to dst itself).
			//nolint:gosec // G305: path traversal is prevented by the filepath.Rel check below.
			resolved := filepath.Join(filepath.Dir(target), hdr.Linkname)
			rel, err := filepath.Rel(filepath.Clean(dst), filepath.Clean(resolved))
			if err != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("symlink %q: target %q escapes destination directory", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			// Metadata-only entries produced by some tar implementations.
			// Go's archive/tar resolves these in Next(), but handle them
			// defensively in case an unusual archive surfaces them directly.
			continue
		default:
			return fmt.Errorf("unsupported tar entry type %d for %q", hdr.Typeflag, hdr.Name)
		}
	}
	return nil
}

// safeJoin joins base and name, returning an error if the result would escape base.
func safeJoin(base, name string) (string, error) {
	// filepath.Clean("/"+name) produces an absolute path; filepath.Join then
	// discards base on Unix-like systems. Strip the leading separator after
	// cleaning so that the result stays relative to base.
	clean := filepath.Clean(string(os.PathSeparator) + name)
	rel := strings.TrimPrefix(clean, string(os.PathSeparator))
	target := filepath.Join(base, rel)
	cleanBase := filepath.Clean(base)
	if !strings.HasPrefix(target, cleanBase+string(os.PathSeparator)) &&
		target != cleanBase {
		return "", fmt.Errorf("archive path %q escapes destination directory", name)
	}
	return target, nil
}

// archive/zip requires a ReaderAt, so we use an *os.File as the backing store
// to avoid buffering the entire archive in memory.
func extractZipFromReader(dst string, r io.Reader) error {
	f, err := os.CreateTemp("", "bundle-*.zip")
	if err != nil {
		return fmt.Errorf("creating temp file for zip: %w", err)
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("writing zip body to temp file: %w", err)
	}

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("seeking zip temp file: %w", err)
	}

	zr, err := zip.NewReader(f, size)
	if err != nil {
		return fmt.Errorf("opening zip reader: %w", err)
	}

	for _, entry := range zr.File {
		target, err := safeJoin(dst, entry.Name)
		if err != nil {
			return err
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0750); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return err
		}

		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("opening zip entry %q: %w", entry.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, entry.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		//nolint:gosec // G110: archive content is sourced from a trusted server configured by the cluster admin
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return fmt.Errorf("extracting zip entry %q: %w", entry.Name, err)
		}
		rc.Close()
		out.Close()
	}
	return nil
}
