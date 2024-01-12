package crd

import (
	"context"
	"io"
	"os"
	"path/filepath"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/crd"
	"github.com/rancher/wrangler/v2/pkg/schemas/openapi"
	"github.com/rancher/wrangler/v2/pkg/yaml"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func Create(ctx context.Context, cfg *rest.Config) error {
	factory, err := crd.NewFactoryFromClient(cfg)
	if err != nil {
		return err
	}

	return factory.BatchCreateCRDs(ctx, list()...).BatchWait()
}

func WriteFile(filename string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	return print(f)
}

func print(out io.Writer) error {
	obj, err := objects()
	if err != nil {
		return err
	}
	data, err := yaml.Export(obj...)
	if err != nil {
		return err
	}

	_, err = out.Write(data)
	return err
}

func objects() (result []runtime.Object, err error) {
	for _, crdDef := range list() {
		crd, err := crdDef.ToCustomResourceDefinition()
		if err != nil {
			return nil, err
		}
		result = append(result, crd)
	}
	return
}

func list() []crd.CRD {
	return []crd.CRD{
		newCRD(&fleet.Bundle{}, func(c crd.CRD) crd.CRD {
			schema := mustSchema(fleet.Bundle{})
			schema.Properties["spec"].Properties["helm"].Properties["releaseName"] = releaseNameValidation()

			c.GVK.Kind = "Bundle"
			return c.
				WithSchemaFromStruct(nil).
				WithSchema(schema).
				WithColumn("BundleDeployments-Ready", ".status.display.readyClusters").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.BundleDeployment{}, func(c crd.CRD) crd.CRD {
			schema := mustSchema(fleet.BundleDeployment{})
			schema.Properties["spec"].Properties["options"].Properties["helm"].Properties["releaseName"] = releaseNameValidation()

			c.GVK.Kind = "BundleDeployment"
			return c.
				WithSchemaFromStruct(nil).
				WithSchema(schema).
				WithColumn("Deployed", ".status.display.deployed").
				WithColumn("Monitored", ".status.display.monitored").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.BundleNamespaceMapping{}, func(c crd.CRD) crd.CRD {
			return c
		}),
		newCRD(&fleet.ClusterGroup{}, func(c crd.CRD) crd.CRD {
			return c.
				WithCategories("fleet").
				WithColumn("Clusters-Ready", ".status.display.readyClusters").
				WithColumn("Bundles-Ready", ".status.display.readyBundles").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.Cluster{}, func(c crd.CRD) crd.CRD {
			schema := mustSchema(fleet.Cluster{})
			schema.Properties["metadata"] = metadataNameValidation()
			schema.Properties["spec"].Properties["agentTolerations"].Items.Schema.Properties["tolerationSeconds"] = tolerationSecondsValidation()

			nodeAffinity := nodeAffinity(schema)
			nodeAffinity.Properties["requiredDuringSchedulingIgnoredDuringExecution"].Properties["nodeSelectorTerms"].Items.Schema.Properties["matchExpressions"].Items.Schema.Properties["operator"] = nodeSelectorOperatorValidation()
			nodeAffinity.Properties["requiredDuringSchedulingIgnoredDuringExecution"].Properties["nodeSelectorTerms"].Items.Schema.Properties["matchFields"].Items.Schema.Properties["operator"] = nodeSelectorOperatorValidation()
			nodeAffinity.Properties["preferredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["preference"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = nodeSelectorOperatorValidation()
			nodeAffinity.Properties["preferredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["preference"].Properties["matchFields"].Items.Schema.Properties["operator"] = nodeSelectorOperatorValidation()

			podAffinity := podAffinity(schema)
			podAffinity.Properties["requiredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["labelSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()
			podAffinity.Properties["requiredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["namespaceSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()
			podAffinity.Properties["preferredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["podAffinityTerm"].Properties["labelSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()
			podAffinity.Properties["preferredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["podAffinityTerm"].Properties["namespaceSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()

			podAntiAffinity := podAntiAffinity(schema)
			podAntiAffinity.Properties["requiredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["labelSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()
			podAntiAffinity.Properties["requiredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["namespaceSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()
			podAntiAffinity.Properties["preferredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["podAffinityTerm"].Properties["labelSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()
			podAntiAffinity.Properties["preferredDuringSchedulingIgnoredDuringExecution"].Items.Schema.Properties["podAffinityTerm"].Properties["namespaceSelector"].Properties["matchExpressions"].Items.Schema.Properties["operator"] = labelSelectorOperatorValidation()

			c.GVK.Kind = "Cluster"
			return c.
				WithSchemaFromStruct(nil).
				WithSchema(schema).
				WithColumn("Bundles-Ready", ".status.display.readyBundles").
				WithColumn("Nodes-Ready", ".status.display.readyNodes").
				WithColumn("Sample-Node", ".status.display.sampleNode").
				WithColumn("Last-Seen", ".status.agent.lastSeen").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.ClusterRegistrationToken{}, func(c crd.CRD) crd.CRD {
			schema := mustSchema(fleet.ClusterRegistrationToken{})
			schema.Properties["metadata"] = metadataNameValidation()

			c.GVK.Kind = "ClusterRegistrationToken"
			return c.
				WithSchemaFromStruct(nil).
				WithSchema(schema).
				WithColumn("Secret-Name", ".status.secretName")
		}),
		newCRD(&fleet.GitRepo{}, func(c crd.CRD) crd.CRD {
			return c.
				WithCategories("fleet").
				WithColumn("Repo", ".spec.repo").
				WithColumn("Commit", ".status.commit").
				WithColumn("BundleDeployments-Ready", ".status.display.readyBundleDeployments").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.ClusterRegistration{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Cluster-Name", ".status.clusterName").
				WithColumn("Labels", ".spec.clusterLabels")
		}),
		newCRD(&fleet.GitRepoRestriction{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Default-ServiceAccount", ".defaultServiceAccount").
				WithColumn("Allowed-ServiceAccounts", ".allowedServiceAccounts")
		}),
		newCRD(&fleet.Content{}, func(c crd.CRD) crd.CRD {
			c.NonNamespace = true
			c.Status = false
			return c
		}),
		newCRD(&fleet.ImageScan{}, func(c crd.CRD) crd.CRD {
			return c.WithCategories("fleet").
				WithColumn("Repository", ".spec.image").
				WithColumn("Latest", ".status.latestTag")
		}),
	}
}

func newCRD(obj interface{}, customize func(crd.CRD) crd.CRD) crd.CRD {
	crd := crd.CRD{
		GVK: schema.GroupVersionKind{
			Group:   "fleet.cattle.io",
			Version: "v1alpha1",
		},
		Status:       true,
		SchemaObject: obj,
	}
	if customize != nil {
		crd = customize(crd)
	}
	return crd
}

// metadataNameValidation returns a schema that validates the metadata.name field
// metadata:
//
//	properties:
//	  name:
//	    type: string
//	    pattern: "^[-a-z0-9]*$"
//	    maxLength: 63
//	type: object
func metadataNameValidation() apiextv1.JSONSchemaProps {
	prop := apiextv1.JSONSchemaProps{
		Type:      "string",
		Pattern:   "^[-a-z0-9]+$",
		MaxLength: &[]int64{63}[0],
	}
	return apiextv1.JSONSchemaProps{
		Type:       "object",
		Properties: map[string]apiextv1.JSONSchemaProps{"name": prop},
	}

}

// releaseNameValidation for helm release names according to helm itself
func releaseNameValidation() apiextv1.JSONSchemaProps {
	return apiextv1.JSONSchemaProps{
		Type:      "string",
		Pattern:   `^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`,
		MaxLength: &[]int64{fleet.MaxHelmReleaseNameLen}[0],
		Nullable:  true,
	}
}

// tolerationSecondsValidation limits the maximum of TolerationSeconds to one day
func tolerationSecondsValidation() apiextv1.JSONSchemaProps {
	return apiextv1.JSONSchemaProps{
		Type:     "integer",
		Maximum:  &[]float64{86400}[0],
		Nullable: true,
	}
}

// nodeSelectorOperatorValidation validates the Operator is one of: In, NotIn, Exists, DoesNotExist, Gt, Lt
func nodeSelectorOperatorValidation() apiextv1.JSONSchemaProps {
	return apiextv1.JSONSchemaProps{
		Type:     "string",
		Enum:     []apiextv1.JSON{{Raw: []byte(`"In"`)}, {Raw: []byte(`"NotIn"`)}, {Raw: []byte(`"Exists"`)}, {Raw: []byte(`"DoesNotExist"`)}, {Raw: []byte(`"Gt"`)}, {Raw: []byte(`"Lt"`)}},
		Nullable: true,
	}
}

// labelSelectorOperatorValidation validates the Operator is one of: In, NotIn, Exists, DoesNotExist
func labelSelectorOperatorValidation() apiextv1.JSONSchemaProps {
	return apiextv1.JSONSchemaProps{
		Type:     "string",
		Enum:     []apiextv1.JSON{{Raw: []byte(`"In"`)}, {Raw: []byte(`"NotIn"`)}, {Raw: []byte(`"Exists"`)}, {Raw: []byte(`"DoesNotExist"`)}},
		Nullable: true,
	}
}

func mustSchema(obj interface{}) *apiextv1.JSONSchemaProps {
	result, err := openapi.ToOpenAPIFromStruct(obj)
	if err != nil {
		panic(err)
	}
	return result
}

func agentAffinity(schema *apiextv1.JSONSchemaProps) apiextv1.JSONSchemaProps {
	return schema.Properties["spec"].Properties["agentAffinity"]
}

func nodeAffinity(schema *apiextv1.JSONSchemaProps) apiextv1.JSONSchemaProps {
	return agentAffinity(schema).Properties["nodeAffinity"]
}

func podAffinity(schema *apiextv1.JSONSchemaProps) apiextv1.JSONSchemaProps {
	return agentAffinity(schema).Properties["podAffinity"]
}

func podAntiAffinity(schema *apiextv1.JSONSchemaProps) apiextv1.JSONSchemaProps {
	return agentAffinity(schema).Properties["podAntiAffinity"]
}
