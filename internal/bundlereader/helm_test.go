package bundlereader_test

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	authUsername  = "holadonpepito"
	authPassword  = "holadonjose"
	helmRepoIndex = `apiVersion: v1
entries:
  sleeper:
    - created: 2016-10-06T16:23:20.499814565-06:00
      description: Super sleeper chart
      digest: 99c76e403d752c84ead610644d4b1c2f2b453a74b921f422b9dcb8a7c8b559cd
      home: https://helm.sh/helm
      name: alpine
      sources:
      - https://github.com/helm/helm
      urls:
      - https://##URL##/sleeper-chart-0.1.0.tgz
      version: 0.1.0
generated: 2016-10-06T16:23:20.499029981-06:00`
)

func checksumPrefix(helm *fleet.HelmOptions) string {
	if helm == nil {
		return "none"
	}
	return fmt.Sprintf(".chart/%x", sha256.Sum256([]byte(helm.Chart + ":" + helm.Repo + ":" + helm.Version)[:]))
}

func newTLSServer(index string, withAuth bool) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if withAuth {
			username, password, ok := r.BasicAuth()
			if ok {
				usernameHash := sha256.Sum256([]byte(username))
				passwordHash := sha256.Sum256([]byte(password))
				expectedUsernameHash := sha256.Sum256([]byte(authUsername))
				expectedPasswordHash := sha256.Sum256([]byte(authPassword))

				usernameMatch := (subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1)
				passwordMatch := (subtle.ConstantTimeCompare(passwordHash[:], expectedPasswordHash[:]) == 1)

				if !usernameMatch || !passwordMatch {
					w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
		}

		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/index.yaml" {
			index = strings.Replace(index, "##URL##", r.Host, -1)
			fmt.Fprint(w, index)
		} else if r.URL.Path == "/sleeper-chart-0.1.0.tgz" {
			// chartContents, err := os.ReadFile("assets/sleeper-chart-0.1.0.tgz")
			// if err != nil {
			// 	fmt.Fprint(w, err.Error())
			// } else {
			// 	fmt.Fprint(w, chartContents)
			// }
			f, err := os.Open("assets/sleeper-chart-0.1.0.tgz")
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
			}
			defer f.Close()

			_, err = io.Copy(w, f)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
			}
		}
	}))
	return srv
}

// nolint: funlen
func TestGetManifestFromHelmChart(t *testing.T) {
	cases := []struct {
		name                string
		bd                  fleet.BundleDeployment
		clientCalls         func(*mocks.MockClient)
		requiresAuth        bool
		expectedNilManifest bool
		expectedResources   []fleet.BundleResource
		expectedErrNotNil   bool
		expectedError       string
	}{
		{
			name: "no helm options",
			bd: fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: nil,
					},
				},
			},
			clientCalls:         func(c *mocks.MockClient) {},
			requiresAuth:        false,
			expectedNilManifest: true,
			expectedResources:   []fleet.BundleResource{},
			expectedErrNotNil:   true,
			expectedError:       "helm options not found",
		},
		{
			name: "error reading secret",
			bd: fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{},
					},
					HelmChartOptions: &fleet.BundleHelmOptions{
						SecretName: "secretdoesnotexist",
					},
				},
			},
			clientCalls: func(c *mocks.MockClient) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("secret not found"))
			},
			requiresAuth:        false,
			expectedNilManifest: true,
			expectedResources:   []fleet.BundleResource{},
			expectedErrNotNil:   true,
			expectedError:       "secret not found",
		},
		{
			name: "authentication error",
			bd: fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Repo: "##URL##", // will be replaced by the mock server url
						},
					},
					HelmChartOptions: &fleet.BundleHelmOptions{
						SecretName:            "secretdoesnotexist",
						InsecureSkipTLSverify: true,
					},
				},
			},
			clientCalls: func(c *mocks.MockClient) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, _ types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
						secret.Data = make(map[string][]byte)
						secret.Data[corev1.BasicAuthUsernameKey] = []byte(authUsername)
						secret.Data[corev1.BasicAuthPasswordKey] = []byte("bad password")
						return nil
					},
				)
			},
			requiresAuth:        true,
			expectedNilManifest: true,
			expectedResources:   []fleet.BundleResource{},
			expectedErrNotNil:   true,
			expectedError:       "failed to read helm repo from ##URL##/index.yaml, error code: 401, response body: Unauthorized\n",
		},
		{
			name: "tls error",
			bd: fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Repo: "##URL##", // will be replaced by the mock server url
						},
					},
					HelmChartOptions: &fleet.BundleHelmOptions{
						SecretName:            "secretdoesnotexist",
						InsecureSkipTLSverify: false,
					},
				},
			},
			clientCalls: func(c *mocks.MockClient) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			},
			requiresAuth:        false,
			expectedNilManifest: true,
			expectedResources:   []fleet.BundleResource{},
			expectedErrNotNil:   true,
			expectedError:       "Get \"##URL##/index.yaml\": tls: failed to verify certificate: x509: certificate signed by unknown authority",
		},
		{
			name: "load directory no version specified",
			bd: fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Repo:  "##URL##", // will be replaced by the mock server url
							Chart: "sleeper",
						},
					},
					HelmChartOptions: &fleet.BundleHelmOptions{
						InsecureSkipTLSverify: true,
					},
				},
			},
			clientCalls:         func(c *mocks.MockClient) {},
			requiresAuth:        false,
			expectedNilManifest: false,
			expectedResources: []fleet.BundleResource{
				{
					Name:    "sleeper-chart/templates/deployment.yaml",
					Content: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: sleeper\n  labels:\n    fleet: testing\nspec:\n  replicas: {{ .Values.replicaCount }}\n  selector:\n    matchLabels:\n      app: sleeper\n  template:\n    metadata:\n      {{- with .Values.podAnnotations }}\n      annotations:\n        {{- toYaml . | nindent 8 }}\n      {{- end }}\n      labels:\n        app: sleeper\n    spec:\n      {{- with .Values.imagePullSecrets }}\n      imagePullSecrets:\n        {{- toYaml . | nindent 8 }}\n      {{- end }}\n      securityContext:\n        {{- toYaml .Values.podSecurityContext | nindent 8 }}\n      containers:\n        - name: {{ .Chart.Name }}\n          command:\n            - sleep\n            - 7d\n          securityContext:\n            {{- toYaml .Values.securityContext | nindent 12 }}\n          image: \"{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}\"\n          imagePullPolicy: {{ .Values.image.pullPolicy }}\n      {{- with .Values.nodeSelector }}\n      nodeSelector:\n        {{- toYaml . | nindent 8 }}\n      {{- end }}\n      {{- with .Values.affinity }}\n      affinity:\n        {{- toYaml . | nindent 8 }}\n      {{- end }}\n      {{- with .Values.tolerations }}\n      tolerations:\n        {{- toYaml . | nindent 8 }}\n      {{- end }}\n",
				},
				{
					Name:    "sleeper-chart/values.yaml",
					Content: "replicaCount: 1\n\nimage:\n  repository: rancher/mirrored-library-busybox\n  pullPolicy: IfNotPresent\n  tag: \"1.34.1\"\n\nimagePullSecrets: []\n\npodAnnotations: {}\n\npodSecurityContext: {}\nsecurityContext: {}\n\nnodeSelector: {}\ntolerations: []\naffinity: {}\n",
				},
				{
					Name:    "sleeper-chart/Chart.yaml",
					Content: "apiVersion: v2\nappVersion: 1.16.0\ndescription: A test chart\nname: sleeper-chart\ntype: application\nversion: 0.1.0\n",
				},
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
	}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockClient := mocks.NewMockClient(mockCtrl)

	assert := assert.New(t)
	for _, c := range cases {
		// set expected calls to client mock
		c.clientCalls(mockClient)

		// start mock server for test
		srv := newTLSServer(helmRepoIndex, c.requiresAuth)
		defer srv.Close()

		resourcePrefix := ""
		if c.bd.Spec.Options.Helm != nil {
			c.bd.Spec.Options.Helm.Repo = strings.Replace(c.bd.Spec.Options.Helm.Repo, "##URL##", srv.URL, -1)
			// resource names have a prefix that depends on helm options
			resourcePrefix = checksumPrefix(c.bd.Spec.Options.Helm)
		}
		// change the url in the error in case it is present
		c.expectedError = strings.Replace(c.expectedError, "##URL##", srv.URL, -1)

		manifest, err := bundlereader.GetManifestFromHelmChart(context.TODO(), mockClient, &c.bd)

		assert.Equal(c.expectedNilManifest, manifest == nil)
		assert.Equal(c.expectedErrNotNil, err != nil)
		if err != nil && c.expectedErrNotNil {
			assert.Equal(c.expectedError, err.Error())
		}
		if manifest != nil {
			// check that all expected resources are found
			for _, expectedRes := range c.expectedResources {
				// find the resource in the expected ones
				found := false
				for _, r := range manifest.Resources {
					if fmt.Sprintf("%s/%s", resourcePrefix, expectedRes.Name) == r.Name {
						found = true
						assert.Equal(expectedRes.Content, r.Content)
					}
				}
				if !found {
					t.Errorf("expected resource %s was not found", expectedRes.Name)
				}
			}

			// check that all of the returned resources are also expected
			for _, r := range manifest.Resources {
				// find the resource in the expected ones
				found := false
				for _, expectedRes := range c.expectedResources {
					if fmt.Sprintf("%s/%s", resourcePrefix, expectedRes.Name) == r.Name {
						found = true
						assert.Equal(expectedRes.Content, r.Content)
					}
				}
				if !found {
					t.Errorf("returned resource %s was not expected", r.Name)
				}
			}
		}
	}
}
