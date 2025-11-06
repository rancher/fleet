package helmdeployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v3/pkg/chart"
)

func TestHasLookupFunction(t *testing.T) {
	testCases := []struct {
		name           string
		templates      []*chart.File
		expectedResult bool
	}{
		{
			name:           "Chart with no templates",
			templates:      []*chart.File{},
			expectedResult: false,
		},
		{
			name: "Template without lookup",
			templates: []*chart.File{
				{Name: "templates/deployment.yaml", Data: []byte(`apiVersion: apps/v1\nkind: Deployment`)},
			},
			expectedResult: false,
		},
		{
			name: "Template with simple lookup",
			templates: []*chart.File{
				{Name: "templates/service.yaml", Data: []byte(`{{ lookup "v1" "Service" "default" "kubernetes" }}`)},
			},
			expectedResult: true,
		},
		{
			name: "Template with 'lookup' as text",
			templates: []*chart.File{
				{Name: "templates/configmap.yaml", Data: []byte(`data:\n  key: "some lookup value"`)},
			},
			expectedResult: false,
		},
		{
			name: "Template with 'lookup' in a comment",
			templates: []*chart.File{
				{Name: "templates/configmap.yaml", Data: []byte(`{{- /* This is a lookup function */ -}}`)},
			},
			expectedResult: false,
		},
		{
			name: "Template with lookup in an 'if' block",
			templates: []*chart.File{
				{Name: "templates/if.yaml", Data: []byte(`{{ if .Values.enabled }}{{ lookup "v1" "Pod" "default" "mypod" }}{{ end }}`)},
			},
			expectedResult: true,
		},
		{
			name: "Template with lookup in an 'if' condition",
			templates: []*chart.File{
				{Name: "templates/if.yaml", Data: []byte(`{{ if lookup "v1" "ConfigMap" "default" "my-cm" }}found{{ end }}`)},
			},
			expectedResult: true,
		},
		{
			name: "Template with lookup in a 'range' block",
			templates: []*chart.File{
				{Name: "templates/range.yaml", Data: []byte(`{{ range .Values.items }}{{ lookup "v1" "Secret" .Release.Namespace .Name }}{{ end }}`)},
			},
			expectedResult: true,
		},
		{
			name: "Template with lookup in a 'with' block",
			templates: []*chart.File{
				{Name: "templates/with.yaml", Data: []byte(`{{ with .Values.service }}{{ lookup "v1" "Service" .Namespace .Name }}{{ end }}`)},
			},
			expectedResult: true,
		},
		{
			name: "Template with nested lookup",
			templates: []*chart.File{
				{Name: "templates/nested.yaml", Data: []byte(`{{ $cm := lookup "v1" "ConfigMap" "default" "my-cm" }}{{ if $cm }}{{ lookup "v1" "Secret" "default" "my-secret" }}{{ end }}`)},
			},
			expectedResult: true,
		},
		{
			name: "Template with invalid syntax (should be ignored)",
			templates: []*chart.File{
				{Name: "templates/invalid.yaml", Data: []byte(`{{ .Values.name }`)},
				{Name: "templates/valid.yaml", Data: []byte(`data: "valid"`)},
			},
			expectedResult: false,
		},
		{
			name: "Template with invalid syntax and another with lookup",
			templates: []*chart.File{
				{Name: "templates/invalid.yaml", Data: []byte(`{{ .Values.name }`)},
				{Name: "templates/valid_with_lookup.yaml", Data: []byte(`{{ lookup "v1" "Service" "default" "kubernetes" }}`)},
			},
			expectedResult: true,
		},
		{
			name: "Template with lookup as an argument",
			templates: []*chart.File{
				{Name: "templates/arg.yaml", Data: []byte(`{{- define "myTpl" }}{{ . }}{{ end -}}{{ template "myTpl" (lookup "v1" "Pod" "default" "my-pod") }}`)},
			},
			expectedResult: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			chart := &chart.Chart{
				Metadata: &chart.Metadata{
					Name:    "test-chart",
					Version: "0.1.0",
				},
				Templates: tc.templates,
			}

			assert.Equal(tc.expectedResult, hasLookupFunction(chart))
		})
	}
}
