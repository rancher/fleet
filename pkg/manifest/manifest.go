package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type Manifest struct {
	Resources []fleet.BundleResource `json:"resources,omitempty"`
	raw       []byte
	digest    string
}

func New(spec *fleet.BundleSpec) (*Manifest, error) {
	m := &Manifest{
		Resources: spec.Resources,
	}
	return m, nil
}

func ReadManifest(data []byte, digest string) (*Manifest, error) {
	if digest != "" {
		if _, err := sha256Matches(bytes.NewReader(data), digest); err != nil {
			return nil, err
		}
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Content() ([]byte, string, error) {
	if m.digest != "" {
		return m.raw, m.digest, nil
	}

	buf := &bytes.Buffer{}
	digest := sha256.New()
	out := io.MultiWriter(buf, digest)
	if err := m.Encode(out); err != nil {
		return nil, "", err
	}
	m.raw = buf.Bytes()
	m.digest = toSHA256ID(digest.Sum(nil))
	return m.raw, m.digest, nil
}

func (m *Manifest) Encode(writer io.Writer) error {
	return json.NewEncoder(writer).Encode(m)
}

func toSHA256ID(digest []byte) string {
	return ("s-" + hex.EncodeToString(digest))[:63]
}
