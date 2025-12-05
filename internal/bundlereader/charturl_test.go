//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/oci_client_mock.go -package=mocks oras.land/oras-go/v2/registry/remote Client
package bundlereader_test

import (
	"bytes"
	"context"
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
			name:           "returns empty tag and no error when no candidate is found",
			inputVersion:   "0.*.0",
			respTags:       `{"name": "bar", "tags":["1.1.0", "1.2.0", "1.4.3", "1.9.9"]}`,
			respStatusCode: http.StatusOK,
			expectedTag:    "",
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
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, err := bundlereader.ChartURL(context.Background(), c.options, bundlereader.Auth{}, false)
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}

			if url != c.expected {
				t.Errorf("expected %q, got %q", c.expected, url)
			}
		})
	}
}
