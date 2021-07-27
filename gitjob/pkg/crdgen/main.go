package main

import (
	"fmt"
	"strings"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/crd"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io"
	"github.com/rancher/wrangler/pkg/schemas/openapi"
	"github.com/rancher/wrangler/pkg/yaml"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func main() {
	fmt.Println("{{- if .Capabilities.APIVersions.Has \"apiextensions.k8s.io/v1\" -}}\n" +
		generateGitJobCrd(false) +
		"\n{{- else -}}\n" +
		generateGitJobCrd(true) +
		"\n{{- end -}}")
}

func generateGitJobCrd(v1beta1 bool) string {
	crdObject := crd.NamespacedType("GitJob.gitjob.cattle.io/v1").
		WithStatus().
		WithSchema(mustSchema(gitjobv1.GitJob{})).
		WithColumnsFromStruct(gitjobv1.GitJob{}).
		WithCustomColumn(apiextv1.CustomResourceColumnDefinition{
			Name:     "Age",
			Type:     "date",
			JSONPath: ".metadata.creationTimestamp",
		})
	if v1beta1 {
		runtimeObject, err := crdObject.ToCustomResourceDefinitionV1Beta1()
		if err != nil {
			panic(err)
		}
		return generateYamlString(runtimeObject)
	}
	runtimeObject, err := crdObject.ToCustomResourceDefinition()
	if err != nil {
		panic(err)
	}
	return generateYamlString(runtimeObject)
}

func mustSchema(obj interface{}) *apiextv1.JSONSchemaProps {
	result, err := openapi.ToOpenAPIFromStruct(obj)
	if err != nil {
		panic(err)
	}
	return result
}

func generateYamlString(obj runtime.Object) string {
	crdYaml, err := yaml.Export(obj)
	if err != nil {
		panic(err)
	}
	return strings.Trim(string(crdYaml), "\n")
}
