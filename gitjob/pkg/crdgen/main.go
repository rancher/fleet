package main

import (
	"fmt"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/crd"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io"
	"github.com/rancher/wrangler/pkg/schemas/openapi"
	"github.com/rancher/wrangler/pkg/yaml"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func main() {
	var crds []crd.CRD
	crds = append(crds,
		crd.NamespacedType("GitJob.gitjob.cattle.io/v1").
			WithStatus().
			WithSchema(mustSchema(v1.GitJob{})).
			WithColumnsFromStruct(v1.GitJob{}).
			WithCustomColumn(apiextv1.CustomResourceColumnDefinition{
				Name:     "Age",
				Type:     "date",
				JSONPath: ".metadata.creationTimestamp",
			}))

	var result []runtime.Object
	for _, crd := range crds {
		crdObject, err := crd.ToCustomResourceDefinition()
		if err != nil {
			panic(err)
		}
		result = append(result, crdObject)
	}

	output, err := yaml.Export(result...)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(output))
}

func mustSchema(obj interface{}) *apiextv1.JSONSchemaProps {
	result, err := openapi.ToOpenAPIFromStruct(obj)
	if err != nil {
		panic(err)
	}
	return result
}
