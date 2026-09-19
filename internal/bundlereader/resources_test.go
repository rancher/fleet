package bundlereader

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rancher/fleet/internal/helmversion"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrlog "sigs.k8s.io/controller-runtime/pkg/log"
)

type logRecorder struct {
	lines []string
}

func (r *logRecorder) Helper() {}

func (r *logRecorder) Log(args ...any) {
	r.lines = append(r.lines, fmt.Sprint(args...))
}

const (
	valuesOneYaml = `microService1:
  resources:
    limits:
      cpu: 500m
      memory: 500Mi
    requests:
      cpu: 256m
      memory: 256Mi

microService2:
  resources:
    limits:
      cpu: 500m
      memory: 500Mi
    requests:
      cpu: 256m
      memory: 256Mi
`
	valuesTwoYaml = `microService1:
  replicas: 1
microService2:
  replicas: 2`
)

func TestValueMerge(t *testing.T) {
	first := &fleet.GenericMap{}
	second := &fleet.GenericMap{}

	err := yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(valuesOneYaml)).Decode(first)
	if err != nil {
		t.Fatalf("error during valuesOneYaml parsing %v", err)
	}

	err = yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(valuesTwoYaml)).Decode(second)
	if err != nil {
		t.Fatalf("error during valuesTwoYaml parsing %v", err)
	}

	mergeMap := mergeGenericMap(first, second)

	for _, serviceName := range []string{"microService1", "microService2"} {
		serviceVals, ok := mergeMap.Data[serviceName]
		if !ok {
			t.Fatalf("unable to find parent key for service %s", serviceName)
		}
		resourceVals, ok := serviceVals.(map[string]any)["resources"]
		if !ok {
			t.Fatalf("unable to find key resources in values for service %s", serviceName)
		}

		limitVals, ok := resourceVals.(map[string]any)["limits"]
		if !ok {
			t.Fatalf("unable to find key limits in resources for service %s", serviceName)
		}

		_, ok = limitVals.(map[string]any)["cpu"]
		if !ok {
			t.Fatalf("unable to find key cpu in limits for service %s", serviceName)
		}

		_, ok = limitVals.(map[string]any)["memory"]
		if !ok {
			t.Fatalf("unable to find key memory in limits for service %s", serviceName)
		}

		requestVals, ok := resourceVals.(map[string]any)["requests"]
		if !ok {
			t.Fatalf("unable to find key requests in resources for service %s", serviceName)
		}

		_, ok = requestVals.(map[string]any)["cpu"]
		if !ok {
			t.Fatalf("unable to find key cpu in requests for service %s", serviceName)
		}

		_, ok = requestVals.(map[string]any)["memory"]
		if !ok {
			t.Fatalf("unable to find key memory in requests for service %s", serviceName)
		}
		_, ok = serviceVals.(map[string]any)["replicas"]
		if !ok {
			t.Fatalf("unable to find key replicas in values for service %s", serviceName)
		}
	}
}

func TestGenerateValuesRejectsPathOutsideBase(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "bundle")
	require.NoError(t, os.MkdirAll(base, 0755))

	outsidePath := filepath.Join(dir, "outside-secret.yaml")
	require.NoError(t, os.WriteFile(outsidePath, []byte("leaked: true"), 0644))

	chart := &fleet.HelmOptions{
		GitOpsHelmOptions: fleet.GitOpsHelmOptions{
			ValuesFiles: []string{"../outside-secret.yaml"},
		},
	}

	_, err := generateValues(base, chart)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid values file")
}

func TestGenerateValuesRejectsSymlinkPointingOutsideBase(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "bundle")
	require.NoError(t, os.MkdirAll(base, 0755))

	outsidePath := filepath.Join(dir, "outside-secret.yaml")
	require.NoError(t, os.WriteFile(outsidePath, []byte("leaked: true"), 0644))
	require.NoError(t, os.Symlink(outsidePath, filepath.Join(base, "values.yaml")))

	chart := &fleet.HelmOptions{
		GitOpsHelmOptions: fleet.GitOpsHelmOptions{
			ValuesFiles: []string{"values.yaml"},
		},
	}

	_, err := generateValues(base, chart)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes bundle directory")
}

func TestGenerateValuesReadsFileWithinBase(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(base, "values.yaml"), []byte("foo: bar"), 0644))

	chart := &fleet.HelmOptions{
		GitOpsHelmOptions: fleet.GitOpsHelmOptions{
			ValuesFiles: []string{"values.yaml"},
		},
	}

	valuesMap, err := generateValues(base, chart)
	require.NoError(t, err)
	assert.Equal(t, "bar", valuesMap.Data["foo"])
}

// A relative base (the common case: gitjob invokes fleet apply with base ".")
// combined with a values file that is an absolute symlink resolving inside
// that base must still be accepted.
func TestGenerateValuesAllowsAbsoluteSymlinkWithinRelativeBase(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "actual-values.yaml")
	require.NoError(t, os.WriteFile(target, []byte("foo: bar"), 0644))
	require.NoError(t, os.Symlink(target, filepath.Join(base, "values.yaml")))

	t.Chdir(base)

	chart := &fleet.HelmOptions{
		GitOpsHelmOptions: fleet.GitOpsHelmOptions{
			ValuesFiles: []string{"values.yaml"},
		},
	}

	valuesMap, err := generateValues(".", chart)
	require.NoError(t, err)
	assert.Equal(t, "bar", valuesMap.Data["foo"])
}

func TestShouldAddAuthToRequest(t *testing.T) {
	cases := []struct {
		name             string
		helmRepoURLRegex string
		repo             string
		chart            string
		want             bool
		wantErr          bool
	}{
		{
			name:             "empty regex always returns false",
			helmRepoURLRegex: "",
			repo:             "https://charts.example.com",
			want:             false,
		},
		{
			name:             "regex matches repo URL",
			helmRepoURLRegex: "https://charts\\.example\\.com.*",
			repo:             "https://charts.example.com/stable",
			want:             true,
		},
		{
			name:             "regex does not match repo URL",
			helmRepoURLRegex: "https://charts\\.example\\.com.*",
			repo:             "https://evil.attacker.com",
			want:             false,
		},
		{
			name:             "no repo falls back to chart URL match",
			helmRepoURLRegex: "oci://registry\\.example\\.com.*",
			chart:            "oci://registry.example.com/charts/mychart",
			want:             true,
		},
		{
			name:             "regex does not match URL injected as query parameter",
			helmRepoURLRegex: "https://charts\\.example\\.com.*",
			repo:             "https://evil.attacker.com/?url=https://charts.example.com",
			want:             false,
		},
		{
			name:             "pre-anchored regex still matches",
			helmRepoURLRegex: "^https://charts\\.example\\.com.*",
			repo:             "https://charts.example.com/stable",
			want:             true,
		},
		{
			name:             "alternation anchors both alternatives",
			helmRepoURLRegex: "https://host1\\.example\\.com/.*|https://host2\\.example\\.com/.*",
			repo:             "https://host2.example.com/charts",
			want:             true,
		},
		{
			name:             "alternation does not match unanchored second alternative",
			helmRepoURLRegex: "https://host1\\.example\\.com/.*|https://host2\\.example\\.com/.*",
			repo:             "evil.com?url=https://host2.example.com/x",
			want:             false,
		},
		{
			name:             "invalid regex returns error",
			helmRepoURLRegex: "[invalid",
			repo:             "https://charts.example.com",
			wantErr:          true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := shouldAddAuthToRequest(c.helmRepoURLRegex, c.repo, c.chart)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestAddRemoteChartsStripsCredentials(t *testing.T) {
	// When helmRepoURLRegex is empty, addRemoteCharts must strip Username and
	// Password from the Auth passed to each directory, while keeping transport
	// flags (BasicHTTP, InsecureSkipVerify) intact.
	auth := Auth{
		Username:           "user",
		Password:           "secret",
		SSHPrivateKey:      []byte("fake-ssh-key"),
		BasicHTTP:          true,
		InsecureSkipVerify: true,
	}

	// Use a chart path that does not exist on disk so addRemoteCharts processes
	// it, and an empty Repo so ChartURL returns the chart string directly
	// without a network call.
	charts := []*fleet.HelmOptions{
		{Chart: "/nonexistent/chart"},
	}

	dirs, err := addRemoteCharts(context.Background(), nil, t.TempDir(), charts, auth, "")
	require.NoError(t, err)
	require.Len(t, dirs, 1)

	got := dirs[0].auth
	assert.Empty(t, got.Username, "Username must be stripped when helmRepoURLRegex is empty")
	assert.Empty(t, got.Password, "Password must be stripped when helmRepoURLRegex is empty")
	assert.Nil(t, got.SSHPrivateKey, "SSHPrivateKey must be stripped when helmRepoURLRegex is empty")
	assert.True(t, got.BasicHTTP, "BasicHTTP must be preserved when stripping credentials")
	assert.True(t, got.InsecureSkipVerify, "InsecureSkipVerify must be preserved when stripping credentials")
	assert.True(t, dirs[0].strippedCreds, "directory should be marked when credentials are stripped due to empty helmRepoURLRegex")
}

func TestAddRemoteChartsWarnsMissingRegex(t *testing.T) {
	remoteChart := []*fleet.HelmOptions{{Chart: "/nonexistent/chart"}}
	recorder := &logRecorder{}
	oldLogger := ctrlog.Log
	ctrlog.SetLogger(testr.NewWithInterface(recorder, testr.Options{Verbosity: 1}))
	t.Cleanup(func() {
		ctrlog.SetLogger(oldLogger)
	})

	tests := []struct {
		name        string
		charts      []*fleet.HelmOptions
		auth        Auth
		regex       string
		wantWarning bool
	}{
		{
			name:        "credentials set, regex empty — warn",
			charts:      remoteChart,
			auth:        Auth{Username: "user", Password: "secret"},
			regex:       "",
			wantWarning: true,
		},
		{
			name:        "SSH key set, regex empty — warn",
			charts:      remoteChart,
			auth:        Auth{SSHPrivateKey: []byte("key")},
			regex:       "",
			wantWarning: true,
		},
		{
			name:        "no credentials, regex empty — no warn",
			charts:      remoteChart,
			auth:        Auth{BasicHTTP: true},
			regex:       "",
			wantWarning: false,
		},
		{
			name:        "credentials set, regex provided — no warn",
			charts:      remoteChart,
			auth:        Auth{Username: "user", Password: "secret"},
			regex:       "https://charts\\.example\\.com.*",
			wantWarning: false,
		},
		{
			name:        "credentials set, regex empty, no charts — no warn",
			charts:      nil,
			auth:        Auth{Username: "user", Password: "secret"},
			regex:       "",
			wantWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder.lines = nil

			_, err := addRemoteCharts(context.Background(), nil, t.TempDir(), tt.charts, tt.auth, tt.regex)
			require.NoError(t, err)

			if tt.wantWarning {
				assert.Contains(t, strings.Join(recorder.lines, "\n"), "helmRepoURLRegex")
			} else {
				assert.NotContains(t, strings.Join(recorder.lines, "\n"), "helmRepoURLRegex")
			}
		})
	}
}

func TestAddRemoteChartsNormalizesVersions(t *testing.T) {
	// An OCI registry does not accept a plus sign in a tag, so users may spell
	// build metadata with an underscore, the way the tag does. Helm only
	// understands the semver spelling.
	charts := []*fleet.HelmOptions{
		{Chart: "/nonexistent/chart", Version: "109.0.1_up1.0.2"},
		{Chart: "/nonexistent/chart", Version: "109.0.1+up1.0.2"},
		{Chart: "/nonexistent/chart", Version: ">= 1.0.0"},
		{Chart: "/nonexistent/chart"},
	}

	dirs, err := addRemoteCharts(context.Background(), nil, t.TempDir(), charts, Auth{}, ".*")
	require.NoError(t, err)
	require.Len(t, dirs, len(charts))

	expected := []string{"109.0.1+up1.0.2", "109.0.1+up1.0.2", ">= 1.0.0", ""}
	for i, want := range expected {
		assert.Equal(t, want, dirs[i].version, "version passed to the downloader for chart %d", i)
		assert.Equal(t, want, charts[i].Version, "version stored in the bundle spec for chart %d", i)
	}
}

func TestAddRemoteChartsResolvesOCIVersions(t *testing.T) {
	// An OCI registry publishes a chart whose version carries build metadata under
	// a tag holding an underscore, and Helm asks a registry for the tag a fully
	// specified version names without ever listing the tags it has.
	tags := `{"name":"sleeper-chart","tags":["109.0.0_up1.0.1","109.0.1_up1.0.1","109.0.1_up1.0.10"]}`

	cases := []struct {
		name            string
		version         string
		listsTags       bool
		expectedVersion string
	}{
		{
			name:            "resolves a version naming no build to the tag publishing it",
			version:         "109.0.1",
			listsTags:       true,
			expectedVersion: "109.0.1+up1.0.10",
		},
		{
			name:            "resolves a version naming a build",
			version:         "109.0.1_up1.0.1",
			listsTags:       true,
			expectedVersion: "109.0.1+up1.0.1",
		},
		{
			name:            "resolves a constraint",
			version:         ">= 109.0.0",
			listsTags:       true,
			expectedVersion: "109.0.1+up1.0.10",
		},
		{
			// Resolution is an improvement on what Helm does by itself rather than a
			// requirement, so Helm is left to ask for the tag the version names, as
			// it did before this resolution existed. A tag semver only accepts
			// loosely deploys this way, as a chart read here is carried in the
			// bundle and never goes through the strict check a HelmOp's version does.
			name:            "keeps the version when no tag matches",
			version:         "109.0.2",
			listsTags:       true,
			expectedVersion: "109.0.2",
		},
		{
			name:            "keeps the version when the registry does not list its tags",
			version:         "109.0.1",
			listsTags:       false,
			expectedVersion: "109.0.1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !c.listsTags || !strings.HasSuffix(r.URL.Path, "/tags/list") {
					w.WriteHeader(http.StatusForbidden)

					return
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tags))
			}))
			defer svr.Close()

			chart := &fleet.HelmOptions{
				Repo:    ociURLPrefix + strings.TrimPrefix(svr.URL, "http://") + "/sleeper-chart",
				Version: c.version,
			}

			dirs, err := addRemoteCharts(
				context.Background(),
				nil,
				t.TempDir(),
				[]*fleet.HelmOptions{chart},
				Auth{BasicHTTP: true},
				".*",
			)

			require.NoError(t, err)
			require.Len(t, dirs, 1)
			assert.Equal(t, c.expectedVersion, dirs[0].version, "version passed to the downloader")

			// The bundle keeps the version the user wrote; only its spelling is
			// normalized.
			assert.Equal(t, helmversion.Normalize(c.version), chart.Version, "version stored in the bundle spec")
		})
	}
}
