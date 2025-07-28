// Package manifest manages content resources, which contain all the resources for a deployed bundle.
//
// Content resources are not namespaced.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type Manifest struct {
	Commit    string                 `json:"-"`
	Resources []fleet.BundleResource `json:"resources,omitempty"`
	shasum    string
	raw       []byte
}

func New(resources []fleet.BundleResource) *Manifest {
	return &Manifest{
		Resources: resources,
	}
}

func FromBundle(bundle *fleet.Bundle) *Manifest {
	return &Manifest{
		Resources: bundle.Spec.Resources,
		shasum:    bundle.Status.ResourcesSHA256Sum,
	}
}

func FromJSON(data []byte, expectedSHAsum string) (*Manifest, error) {
	var m Manifest
	if err := json.NewDecoder(bytes.NewBuffer(data)).Decode(&m); err != nil {
		return nil, err
	}
	m.raw = data
	// #3807 Writing all the data to the hasher to avoid unprocessed data issues.
	// See full details in #3807
	h := sha256.New()
	h.Write(data)
	m.shasum = hex.EncodeToString(h.Sum(nil))

	if expectedSHAsum != "" && expectedSHAsum != m.shasum {
		return nil, fmt.Errorf("content does not match hash got %s, expected %s", m.shasum, expectedSHAsum)
	}

	return &m, nil
}

// encodeManifest serializes the provided Manifest and returns the byte array and its sha256sum
func encodeManifest(m *Manifest) ([]byte, string, error) {
	var buf bytes.Buffer
	h := sha256.New()
	out := io.MultiWriter(&buf, h)

	if err := json.NewEncoder(out).Encode(m); err != nil {
		return nil, "", err
	}

	return buf.Bytes(), hex.EncodeToString(h.Sum(nil)), nil
}

func (m *Manifest) load() error {
	data, shasum, err := encodeManifest(m)
	if err != nil {
		return err
	}
	m.raw = data
	m.shasum = shasum
	return nil
}

// Content retrieves the JSON serialization of the bundle resources
func (m *Manifest) Content() ([]byte, error) {
	if m.raw == nil {
		if err := m.load(); err != nil {
			return nil, err
		}
	}
	return m.raw, nil
}

// ResetSHASum removes stored data about calculated SHASum, forcing a recalculation on the next call to SHASum()
func (m *Manifest) ResetSHASum() {
	m.shasum = ""
}

// SHASum returns the SHA256 sum of the JSON serialization. If necessary it
// loads the content resource and caches its shasum and raw data.
func (m *Manifest) SHASum() (string, error) {
	if m.shasum == "" {
		if err := m.load(); err != nil {
			return "", err
		}
	}
	return m.shasum, nil
}

// ID returns the name of the Content resource produced from this Manifest.
// If necessary it loads the content resource and caches its shasum and
// raw data.
func (m *Manifest) ID() (string, error) {
	shasum, err := m.SHASum()
	if err != nil {
		return "", err
	}
	return ToSHA256ID(shasum), nil
}

// ToSHA256ID generates a valid Kubernetes name (max length of 64) from a provided SHA256 sum
func ToSHA256ID(shasum string) string {
	return ("s-" + shasum)[:63]
}
