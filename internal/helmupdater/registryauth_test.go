package helmupdater

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v2chart "helm.sh/helm/v4/pkg/chart/v2"
)

func applyTo(prefix string) func(string) (bool, error) {
	return func(repoURL string) (bool, error) {
		return strings.HasPrefix(repoURL, prefix), nil
	}
}

func TestOCIHostsNeedingAuth(t *testing.T) {
	deps := []*v2chart.Dependency{
		{Name: "http-dep", Repository: "https://charts.example.com"},
		{Name: "grafana", Repository: "oci://registry-1.docker.io/acme"},
		{Name: "loki", Repository: "oci://registry-1.docker.io/acme"},
		{Name: "tempo", Repository: "oci://registry-1.docker.io/other"},
		{Name: "local", Repository: "oci://registry.internal:5000/charts"},
		{Name: "other", Repository: "oci://ghcr.io/other"},
	}

	cases := []struct {
		name  string
		auth  *RegistryAuth
		hosts []string
		err   string
	}{
		{
			name: "no auth",
		},
		{
			name: "no credentials",
			auth: &RegistryAuth{Applies: applyTo("oci://")},
		},
		{
			name: "no matcher",
			auth: &RegistryAuth{Username: "user", Password: "pass"},
		},
		{
			name:  "matching OCI registries, each once, http repositories ignored",
			auth:  &RegistryAuth{Username: "user", Password: "pass", Applies: applyTo("oci://registry")},
			hosts: []string{"registry-1.docker.io", "registry.internal:5000"},
		},
		{
			name: "nothing matches",
			auth: &RegistryAuth{Username: "user", Password: "pass", Applies: applyTo("oci://quay.io")},
		},
		{
			name: "registry with a matching and a non-matching dependency is left out",
			auth: &RegistryAuth{Username: "user", Password: "pass", Applies: func(repoURL string) (bool, error) {
				return repoURL == "oci://registry-1.docker.io/acme" || strings.HasPrefix(repoURL, "oci://registry.internal"), nil
			}},
			hosts: []string{"registry.internal:5000"},
		},
		{
			name: "matcher error",
			auth: &RegistryAuth{Username: "user", Password: "pass", Applies: func(string) (bool, error) {
				return false, errors.New("bad regex")
			}},
			err: "dependency grafana of chart parent: bad regex",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hosts, err := ociHostsNeedingAuth("parent", deps, c.auth)
			if c.err != "" {
				require.ErrorContains(t, err, c.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.hosts, hosts)
		})
	}
}

type recordedRequest struct {
	path     string
	username string
	password string
}

// fakeRegistry answers the registry calls made while updating a chart's OCI
// dependencies: it wants basic auth on every request and knows one tag.
type fakeRegistry struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, _ := r.BasicAuth()
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{path: r.URL.Path, username: username, password: password})
	f.mu.Unlock()

	if username != "user" || password != "pass" {
		w.Header().Set("WWW-Authenticate", `Basic realm="fleet-test"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/v2/":
		w.WriteHeader(http.StatusOK)
	case strings.HasSuffix(r.URL.Path, "/tags/list"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"charts/dep","tags":["1.0.0"]}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeRegistry) authenticated(pathPrefix string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, r := range f.requests {
		if strings.HasPrefix(r.path, pathPrefix) && r.username == "user" && r.password == "pass" {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeRegistry) all() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

// chartWithOCIDependency writes a chart whose single dependency lives in the
// given registry and has not been downloaded yet.
func chartWithOCIDependency(t *testing.T, registryHost string) string {
	t.Helper()
	dir := t.TempDir()
	chartYAML := fmt.Sprintf(`apiVersion: v2
name: parent
version: 0.1.0
dependencies:
  - name: dep
    version: 1.0.0
    repository: oci://%s/charts
`, registryHost)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ChartYaml), []byte(chartYAML), 0600))
	return dir
}

// isolateHelm keeps Helm's caches and configuration inside the test.
func isolateHelm(t *testing.T) {
	t.Helper()
	for _, env := range []string{"HELM_CACHE_HOME", "HELM_CONFIG_HOME", "HELM_DATA_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
		t.Setenv(env, t.TempDir())
	}
	for _, env := range []string{"HELM_REGISTRY_CONFIG", "HELM_REPOSITORY_CONFIG", "HELM_REPOSITORY_CACHE"} {
		t.Setenv(env, "")
	}
}

func TestUpdateHelmDependenciesLogsInToDependencyRegistry(t *testing.T) {
	isolateHelm(t)
	reg := &fakeRegistry{}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	dir := chartWithOCIDependency(t, host)

	auth := &RegistryAuth{
		Username:  "user",
		Password:  "pass",
		PlainHTTP: true,
		Applies:   applyTo("oci://" + host),
	}
	// The fake registry serves no manifest, so the update itself fails; what
	// matters is that the dependency was requested with the credentials.
	err := UpdateHelmDependencies(dir, auth)
	require.Error(t, err)

	assert.NotEmpty(t, reg.authenticated("/v2/"), "expected a login to %s, got %v", host, reg.all())
	assert.NotEmpty(t, reg.authenticated("/v2/charts/dep"), "expected the dependency to be fetched with credentials, got %v", reg.all())
}

func TestUpdateHelmDependenciesWithoutMatchingCredentialsDoesNotLogIn(t *testing.T) {
	isolateHelm(t)
	reg := &fakeRegistry{}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	dir := chartWithOCIDependency(t, host)

	auth := &RegistryAuth{
		Username:  "user",
		Password:  "pass",
		PlainHTTP: true,
		Applies:   applyTo("oci://registry.example.com"),
	}
	err := UpdateHelmDependencies(dir, auth)
	require.Error(t, err)

	for _, r := range reg.all() {
		assert.Empty(t, r.username, "request %s carried credentials", r.path)
	}
	assert.Empty(t, reg.authenticated("/v2/"))
}

func TestOCIHostsNeedingAuthRejectsBadRepositoryURL(t *testing.T) {
	deps := []*v2chart.Dependency{{Name: "dep", Repository: "oci://bad host/charts"}}
	auth := &RegistryAuth{Username: "user", Password: "pass", Applies: applyTo("oci://")}
	_, err := ociHostsNeedingAuth("parent", deps, auth)
	var urlErr *url.Error
	require.ErrorAs(t, err, &urlErr)
}
