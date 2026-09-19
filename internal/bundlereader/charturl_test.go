//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/oci_client_mock.go -package=mocks -mock_names=Client=MockOCIClient oras.land/oras-go/v2/registry/remote Client
package bundlereader_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/mocks"
	"go.uber.org/mock/gomock"

	"oras.land/oras-go/v2/registry/remote"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func Test_getOCITag(t *testing.T) {
	cases := []struct {
		name           string
		inputVersion   string
		respTags       string
		respStatusCode int
		expectedTag    string
		expectedErrMsg string
	}{
		{
			name:           "finds exact match",
			inputVersion:   "0.2.0",
			respTags:       `{"name": "bar", "tags":["0.1.0", "0.2.0", "0.3.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "0.2.0",
		},
		{
			name:           "finds exact match in reversed output",
			inputVersion:   "0.2.0",
			respTags:       `{"name": "bar", "tags":["0.3.0", "0.2.0", "0.1.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "0.2.0",
		},
		{
			name:           "finds highest match for constraint",
			inputVersion:   "0.*.0",
			respTags:       `{"name": "bar", "tags":["0.1.0", "0.1.9", "0.2.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "0.2.0",
		},
		{
			name:           "finds highest match for constraint with comparisons",
			inputVersion:   "> 0.1.0, <= 1.0.0",
			respTags:       `{"name": "bar", "tags":["0.1.0", "0.2.0", "0.4.3", "0.9.9", "1.0.1"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "0.9.9",
		},
		{
			name:           "errors when no candidate is found",
			inputVersion:   "0.*.0",
			respTags:       `{"name": "bar", "tags":["1.1.0", "1.2.0", "1.4.3", "1.9.9"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "",
			expectedErrMsg: `no tag matching version "0.*.0" found`,
		},
		{
			name:           "ignores tags which are not versions",
			inputVersion:   "> 0.1.0",
			respTags:       `{"name": "bar", "tags":["0.2.0", "latest", "my_tag"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "0.2.0",
		},
		{
			// A version semver only accepts loosely cannot be deployed, so resolving
			// to one would only trade this error for a later one naming a version the
			// user never asked for.
			name:           "ignores tags holding a version semver only accepts loosely",
			inputVersion:   "1.2.0",
			respTags:       `{"name": "bar", "tags":["1.2", "1.2.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "1.2.0",
		},
		{
			name:           "ignores tags holding a version semver only accepts loosely for a constraint",
			inputVersion:   ">= 1.0.0",
			respTags:       `{"name": "bar", "tags":["1.2", "1.2.0", "1.02.3", "3"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "1.2.0",
		},
		{
			name:           "errors when every tag holds a version semver only accepts loosely",
			inputVersion:   "1.2.0",
			respTags:       `{"name": "bar", "tags":["1.2"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "",
			expectedErrMsg: `no tag matching version "1.2.0" found`,
		},
		{
			// Some Helm repositories publish their versions with a "v" prefix, and
			// Fleet accepts those
			name:           "resolves a v-prefixed tag",
			inputVersion:   "1.2.0",
			respTags:       `{"name": "bar", "tags":["v1.1.0", "v1.2.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "v1.2.0",
		},
		{
			name:           "prefers the bare spelling of a version published under two tags",
			inputVersion:   "1.2.0",
			respTags:       `{"name": "bar", "tags":["v1.2.0", "1.2.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "1.2.0",
		},
		{
			name:           "resolves an empty version to the highest available tag",
			inputVersion:   "",
			respTags:       `{"name": "bar", "tags":["0.1.0", "0.3.0", "0.2.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "0.3.0",
		},
		{
			name:           "finds exact match for a version with build metadata",
			inputVersion:   "109.0.1+up1.0.2",
			respTags:       `{"name": "bar", "tags":["109.0.0_up1.0.1", "109.0.1_up1.0.2"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "109.0.1+up1.0.2",
		},
		{
			name:           "finds exact match for a version with build metadata spelled as a tag",
			inputVersion:   "109.0.1_up1.0.2",
			respTags:       `{"name": "bar", "tags":["109.0.0_up1.0.1", "109.0.1_up1.0.2"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "109.0.1+up1.0.2",
		},
		{
			name:           "errors when an exact version with build metadata has no tag",
			inputVersion:   "109.0.1+up1.0.3",
			respTags:       `{"name": "bar", "tags":["109.0.1_up1.0.1", "109.0.1_up1.0.2"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "",
			expectedErrMsg: `no tag matching version "109.0.1+up1.0.3" found`,
		},
		{
			name:           "resolves an exact version to its highest build",
			inputVersion:   "109.0.1",
			respTags:       `{"name": "bar", "tags":["109.0.1_up1.0.2", "109.0.1_up1.0.1", "108.0.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "109.0.1+up1.0.2",
		},
		{
			name:           "prefers a build over the bare version it builds",
			inputVersion:   "109.0.1",
			respTags:       `{"name": "bar", "tags":["109.0.1_up1.0.2", "109.0.1", "109.0.1_up1.0.1"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "109.0.1+up1.0.2",
		},
		{
			name:           "resolves an exact version to the bare tag when it has no build",
			inputVersion:   "109.0.1",
			respTags:       `{"name": "bar", "tags":["109.0.1", "109.0.2_up1.0.1"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "109.0.1",
		},
		{
			name:           "does not resolve an exact version to a build of another version",
			inputVersion:   "109.0.1",
			respTags:       `{"name": "bar", "tags":["109.0.0_up1.0.1", "109.0.2_up1.0.1"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "",
			expectedErrMsg: `no tag matching version "109.0.1" found`,
		},
		{
			name:           "resolves a constraint to the highest build metadata variant",
			inputVersion:   ">= 1.0.0",
			respTags:       `{"name": "bar", "tags":["1.1.0", "1.1.2_build01", "1.1.2_build02"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "1.1.2+build02",
		},
		{
			name:           "resolves a constraint to the highest build metadata variant in reversed output",
			inputVersion:   ">= 1.0.0",
			respTags:       `{"name": "bar", "tags":["1.1.2_build02", "1.1.2_build01", "1.1.0"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "1.1.2+build02",
		},
		{
			name:           "compares build metadata identifiers numerically",
			inputVersion:   ">= 1.0.0",
			respTags:       `{"name": "bar", "tags":["109.0.1_up1.0.2", "109.0.1_up1.0.10"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "109.0.1+up1.0.10",
		},
		{
			name:           "prefers a tag with build metadata when resolving a constraint",
			inputVersion:   ">= 1.0.0",
			respTags:       `{"name": "bar", "tags":["1.1.2_build01", "1.1.2", "1.1.2_build02"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "1.1.2+build02",
		},
		{
			name:           "resolves a constraint to a build metadata variant of a higher version",
			inputVersion:   ">= 1.0.0",
			respTags:       `{"name": "bar", "tags":["1.1.0", "1.1.1", "1.1.2_build01"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "1.1.2+build01",
		},
		{
			name:           "errors when the repository is not found",
			inputVersion:   "0.*.0",
			respTags:       `{"errors": [{"code": "MANIFEST_UNKNOWN", "message": "more stuff here"}]}`,
			expectedTag:    "",
			respStatusCode: http.StatusNotFound,
			expectedErrMsg: `failed to get available tags for version "0.*.0": manifest unknown`,
		},
		{
			name:           "outputs other errors",
			inputVersion:   "0.*.0",
			respTags:       `{"error": "blah bleh"}`,
			expectedTag:    "",
			respStatusCode: http.StatusBadRequest,
			expectedErrMsg: `failed to get available tags for version "0.*.0":`,
		},
		{
			name:           "errors when the version constraint is invalid",
			inputVersion:   "   ",
			expectedTag:    "",
			expectedErrMsg: "failed to compute version constraint",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := remote.NewRepository("foo/bar")
			if err != nil {
				t.Errorf("failed to instantiate test repository")
			}

			ctrl := gomock.NewController(t)
			mockCli := mocks.NewMockOCIClient(ctrl)
			r.Client = mockCli

			resp := http.Response{
				Body:       io.NopCloser(bytes.NewBufferString(c.respTags)),
				Request:    &http.Request{},
				StatusCode: c.respStatusCode,
			}

			mockCli.EXPECT().Do(gomock.Any()).Return(&resp, nil).MaxTimes(1)

			tag, err := bundlereader.GetOCITag(context.Background(), r, c.inputVersion)

			if err != nil {
				if len(c.expectedErrMsg) == 0 || !strings.Contains(err.Error(), c.expectedErrMsg) {
					t.Errorf("expected error message containing %q, got %v", c.expectedErrMsg, err)
				}
			} else if err == nil && len(c.expectedErrMsg) != 0 {
				t.Errorf("expected error message containing %q, got nil", c.expectedErrMsg)
			}

			if tag != c.expectedTag {
				t.Errorf("expected tag %q, got %q", c.expectedTag, tag)
			}
		})
	}
}

// A caller has to tell a registry which lists no matching tag from one whose
// tags are out of reach, as the former says the chart is absent rather than
// unknown.
func Test_getOCITagNoMatchingTagError(t *testing.T) {
	r, err := remote.NewRepository("foo/bar")
	if err != nil {
		t.Fatalf("failed to instantiate test repository: %v", err)
	}

	ctrl := gomock.NewController(t)
	mockCli := mocks.NewMockOCIClient(ctrl)
	r.Client = mockCli

	resp := http.Response{
		Body:       io.NopCloser(bytes.NewBufferString(`{"name": "bar", "tags":["1.1.0"]}`)),
		Request:    &http.Request{},
		StatusCode: http.StatusOK,
	}
	mockCli.EXPECT().Do(gomock.Any()).Return(&resp, nil).MaxTimes(1)

	_, err = bundlereader.GetOCITag(context.Background(), r, "1.2.0")

	var noTag *bundlereader.NoMatchingTagError
	if !errors.As(err, &noTag) {
		t.Fatalf("expected a NoMatchingTagError, got %v", err)
	}

	if noTag.Version != "1.2.0" {
		t.Errorf("expected version %q, got %q", "1.2.0", noTag.Version)
	}
}

func Test_ChartURL(t *testing.T) {
	cases := []struct {
		name     string
		options  fleet.HelmOptions
		expected string
	}{
		{
			name: "repo URL with trailing slash",
			options: fleet.HelmOptions{
				Chart:   "rancher",
				Repo:    "https://releases.rancher.com/server-charts/latest/",
				Version: "2.13.0",
			},
			expected: "https://releases.rancher.com/server-charts/latest/rancher-2.13.0.tgz",
		},
		{
			name: "repo URL without trailing slash",
			options: fleet.HelmOptions{
				Chart:   "rancher",
				Repo:    "https://releases.rancher.com/server-charts/latest",
				Version: "2.13.0",
			},
			expected: "https://releases.rancher.com/server-charts/latest/rancher-2.13.0.tgz",
		},
		{
			name: "OCI URL in helm.repo",
			options: fleet.HelmOptions{
				Repo:    "oci://ghcr.io/rancher/fleet-test-configmap-chart",
				Version: "0.1.0",
			},
			expected: "oci://ghcr.io/rancher/fleet-test-configmap-chart",
		},
		{
			name: "OCI URL in helm.chart (backwards compatibility)",
			options: fleet.HelmOptions{
				Chart:   "oci://ghcr.io/rancher/fleet-test-configmap-chart",
				Version: "0.1.0",
			},
			expected: "oci://ghcr.io/rancher/fleet-test-configmap-chart",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, err := bundlereader.ChartURL(context.Background(), c.options, bundlereader.Auth{})
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}

			if url != c.expected {
				t.Errorf("expected %q, got %q", c.expected, url)
			}
		})
	}
}
