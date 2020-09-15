package manifest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"time"

	"github.com/rancher/fleet/pkg/content"
	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/decrypt"
)

func (m *Manifest) ToTarGZ() (io.Reader, error) {
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	w := tar.NewWriter(gz)

	for _, resource := range m.Resources {
		bytes, err := content.Decode(resource.Content, resource.Encoding)
		if err != nil {
			return nil, err
		}

		bytes, err = decryptSOPS(bytes)
		if err != nil {
			return nil, err
		}

		if err := w.WriteHeader(&tar.Header{
			Name:     resource.Name,
			Mode:     0644,
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
			Size:     int64(len(bytes)),
		}); err != nil {
			return nil, err
		}
		_, err = w.Write(bytes)
		if err != nil {
			return nil, err
		}
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	return buf, gz.Close()
}

func decryptSOPS(data []byte) ([]byte, error) {
	if !bytes.Contains(data, []byte("sops:")) {
		return data, nil
	}

	clearText, err := decrypt.Data(data, "yaml")
	if err == sops.MetadataNotFound {
		return data, nil
	} else if err != nil {
		return data, fmt.Errorf("failed to decrypt with sops: %w", err)
	}
	return clearText, nil
}
