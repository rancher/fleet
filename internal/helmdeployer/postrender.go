package helmdeployer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"helm.sh/helm/v4/pkg/kube"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/helmdeployer/kustomize"
	"github.com/rancher/fleet/internal/helmdeployer/rawyaml"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"

	"k8s.io/apimachinery/pkg/api/meta"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"
)

const CRDKind = "CustomResourceDefinition"

var (
	yamlDocMarker    = []byte("---")
	newlineDocMarker = []byte("\n---")
)

// isYAMLDocMarker reports whether b starts with a valid YAML document-start
// marker: "---" followed by whitespace, CR, LF, or end of input. Per the
// YAML spec, "---" is only a marker when it is the complete token on a line;
// a sequence like "---foo" is NOT a marker and must not be stripped.
func isYAMLDocMarker(b []byte) bool {
	if !bytes.HasPrefix(b, yamlDocMarker) {
		return false
	}
	if len(b) == len(yamlDocMarker) {
		return true
	}
	c := b[len(yamlDocMarker)]
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// normalizeFlowStyleDocs converts YAML documents that start with '{' but are
// not valid JSON (i.e. YAML flow-style with unquoted keys, as emitted by
// Helm v4's kyaml when templates use the toJson function) into block-style
// YAML. k8s.io/apimachinery's ToJSON treats any '{'-prefixed document as
// already-valid JSON and returns it unchanged, so a flow-style document with
// unquoted keys causes json.Unmarshal to fail with "invalid character".
//
// If no document boundary in data starts with '{', data is returned unchanged
// with no allocations. When flow-style documents are present, all documents
// are re-serialized into a consistent "---\n<content>\n" format; documents
// that are not flow-style YAML are passed through without content changes.
func normalizeFlowStyleDocs(data []byte) ([]byte, error) {
	if !hasFlowStyleCandidate(data) {
		return data, nil
	}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	var out bytes.Buffer
	for {
		doc, err := reader.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		// The YAMLReader may include the document-start marker ("---") as part
		// of the document bytes when it appears at the beginning of the input
		// stream. Strip it to obtain the raw content of the document. Only
		// strip a marker that is valid per YAML (followed by whitespace/EOF).
		content := bytes.TrimSpace(doc)
		if isYAMLDocMarker(content) {
			content = bytes.TrimLeft(content[len(yamlDocMarker):], " \t\r\n")
		}
		if len(content) == 0 {
			continue
		}
		if content[0] == '{' && !json.Valid(content) {
			var obj any
			if err := sigsyaml.Unmarshal(content, &obj); err != nil {
				return nil, err
			}
			content, err = sigsyaml.Marshal(obj)
			if err != nil {
				return nil, err
			}
		}
		out.WriteString("---\n")
		out.Write(content)
		if content[len(content)-1] != '\n' {
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

// hasFlowStyleCandidate reports whether data contains at least one YAML
// document whose first non-whitespace byte is '{'. It is a fast, zero-
// allocation scan used as a pre-check before the more expensive processing
// in normalizeFlowStyleDocs.
func hasFlowStyleCandidate(data []byte) bool {
	firstNonSpace := func(b []byte) byte {
		for _, c := range b {
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				return c
			}
		}
		return 0
	}
	rest := data
	// When data begins with a valid YAML document-start marker ("---" followed
	// by whitespace), skip past it so the content of the first document is
	// checked rather than the marker itself. CRLF before the marker is handled
	// by TrimLeft.
	if trimmed := bytes.TrimLeft(data, " \t\r\n"); isYAMLDocMarker(trimmed) {
		rest = trimmed[len(yamlDocMarker):]
	}
	for {
		if firstNonSpace(rest) == '{' {
			return true
		}
		// Searching for "\n---" also covers CRLF separators ("\r\n---") because
		// "\n---" is a substring of "\r\n---".
		i := bytes.Index(rest, newlineDocMarker)
		if i < 0 {
			return false
		}
		rest = rest[i+len(newlineDocMarker):]
	}
}

type postRender struct {
	labelPrefix string
	labelSuffix string
	bundleID    string
	manifest    *manifest.Manifest
	chart       *chartv2.Chart
	mapper      meta.RESTMapper
	opts        fleet.BundleDeploymentOptions
}

func (p *postRender) Run(renderedManifests *bytes.Buffer) (modifiedManifests *bytes.Buffer, err error) {
	data := renderedManifests.Bytes()

	data, err = normalizeFlowStyleDocs(data)
	if err != nil {
		return nil, err
	}

	objs, err := yaml.ToObjects(bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	if len(objs) == 0 {
		data = nil
	}

	// Kustomize applies some restrictions fleet does not have, like a regular expression, which checks for valid file
	// names. If no instructions for kustomize are found in the manifests, then kustomize shouldn't be called at all
	// to prevent causing issues with these restrictions.
	kustomizable := false
	for _, resource := range p.manifest.Resources {
		if strings.HasSuffix(resource.Name, "kustomization.yaml") ||
			strings.HasSuffix(resource.Name, "kustomization.yml") ||
			strings.HasSuffix(resource.Name, "Kustomization") {
			kustomizable = true
			break
		}
	}
	if kustomizable {
		newObjs, processed, err := kustomize.Process(p.manifest, data, p.opts.Kustomize.Dir)
		if err != nil {
			return nil, err
		}
		if processed {
			objs = newObjs
		}
	}

	yamlObjs, err := rawyaml.ToObjects(p.chart)
	if err != nil {
		return nil, err
	}
	objs = append(objs, yamlObjs...)

	setID := desiredset.GetSetID(p.bundleID, p.labelPrefix, p.labelSuffix)
	labels, annotations, err := desiredset.GetLabelsAndAnnotations(setID)
	if err != nil {
		return nil, err
	}

	for _, obj := range objs {
		m, err := meta.Accessor(obj)
		if err != nil {
			return nil, err
		}
		objAnnotations := mergeMaps(m.GetAnnotations(), annotations)
		if !p.opts.DeleteCRDResources &&
			obj.GetObjectKind().GroupVersionKind().Kind == CRDKind {
			objAnnotations[kube.ResourcePolicyAnno] = kube.KeepPolicy
		}
		m.SetLabels(mergeMaps(m.GetLabels(), labels))
		m.SetAnnotations(objAnnotations)

		if p.opts.TargetNamespace != "" {
			if p.mapper != nil {
				gvk := obj.GetObjectKind().GroupVersionKind()
				mapping, err := p.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
				if err != nil {
					return nil, err
				}
				if mapping.Scope.Name() == meta.RESTScopeNameRoot {
					apiVersion, kind := gvk.ToAPIVersionAndKind()
					return nil, fmt.Errorf("invalid cluster scoped object [name=%s kind=%v apiVersion=%s] found. "+
						"Your config uses targetNamespace or namespace and thus forbids cluster-scoped resources. "+
						"If you do not intend to disallow cluster scoped resources, you could switch to defaultNamespace",
						m.GetName(),
						kind, apiVersion)
				}
			}
			m.SetNamespace(p.opts.TargetNamespace)
		}
	}

	data, err = yaml.ToBytes(objs)
	return bytes.NewBuffer(data), err
}
