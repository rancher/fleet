package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/rancher/fleet/pkg/overlay"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/seen"
)

type Manifest struct {
	Resources []fleet.BundleResource `json:"resources,omitempty"`
	raw       []byte
	digest    string
}

func New(spec *fleet.BundleSpec, overlays ...string) (*Manifest, error) {
	resources, err := collectResources(spec, overlays...)
	if err != nil {
		return nil, err
	}
	m := &Manifest{
		Resources: resources,
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

func addResource(result []fleet.BundleResource, seen seen.Seen, resources ...fleet.BundleResource) []fleet.BundleResource {
	for _, resource := range resources {
		if resource.Name != "" && seen.String(resource.Name) {
			continue
		}
		result = append(result, resource)
	}
	return result
}

func collectResources(spec *fleet.BundleSpec, overlays ...string) ([]fleet.BundleResource, error) {
	var (
		seenResources = seen.New()
		result        []fleet.BundleResource
	)

	allOverlays, overlaySet, err := overlay.Resolve(spec, overlays...)
	if err != nil {
		return nil, err
	}

	for i := len(overlaySet) - 1; i >= 0; i-- {
		overlay := allOverlays[overlaySet[i]]
		result = addResource(result, seenResources, overlay.Resources...)
	}

	return addResource(result, seenResources, spec.Resources...), nil
}
