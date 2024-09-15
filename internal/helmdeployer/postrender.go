package helmdeployer

import (
	"bytes"
	"fmt"
	"strings"

	"helm.sh/helm/v3/pkg/kube"

	"helm.sh/helm/v3/pkg/chart"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/applied"
	"github.com/rancher/fleet/internal/helmdeployer/kustomize"
	"github.com/rancher/fleet/internal/helmdeployer/rawyaml"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"

	"k8s.io/apimachinery/pkg/api/meta"
)

const CRDKind = "CustomResourceDefinition"

type postRender struct {
	labelPrefix string
	labelSuffix string
	bundleID    string
	manifest    *manifest.Manifest
	chart       *chart.Chart
	mapper      meta.RESTMapper
	opts        fleet.BundleDeploymentOptions
}

func (p *postRender) Run(renderedManifests *bytes.Buffer) (modifiedManifests *bytes.Buffer, err error) {
	data := renderedManifests.Bytes()

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

	setID := applied.GetSetID(p.bundleID, p.labelPrefix, p.labelSuffix)
	labels, annotations, err := applied.GetLabelsAndAnnotations(setID, nil)
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
