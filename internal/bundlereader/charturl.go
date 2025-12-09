package bundlereader

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"golang.org/x/sync/singleflight"
	repov1 "helm.sh/helm/v4/pkg/repo/v1"
	"sigs.k8s.io/yaml"

	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

const (
	// safety timeout to prevent unbounded requests
	httpClientTimeout = 5 * time.Minute
)

var (
	concurrentIndexFetch singleflight.Group
	transportsCache      = map[string]http.RoundTripper{}
	transportsCacheMutex sync.RWMutex
)

// ChartVersion returns the version of the helm chart from a helm repo server, by
// inspecting the repo's index.yaml
func ChartVersion(ctx context.Context, location fleet.HelmOptions, a Auth) (string, error) {
	if repoURI, ok := strings.CutPrefix(location.Repo, ociURLPrefix); ok {
		client, err := getOCIRepoClient(repoURI, a)
		if err != nil {
			return "", err
		}

		tag, err := GetOCITag(ctx, client, location.Version)
		if len(tag) == 0 || err != nil {
			return "", fmt.Errorf(
				"could not find tag matching constraint %q in registry %s: %w",
				location.Version,
				location.Repo,
				err,
			)
		}
		return tag, nil
	}

	repoURL := location.Repo
	if repoURL == "" {
		return location.Version, nil
	}

	repoIndex, err := getHelmRepoIndex(ctx, repoURL, a)
	if err != nil {
		return "", err
	}
	chart, err := repoIndex.Get(location.Chart, location.Version)
	if err != nil {
		return "", err
	}

	if len(chart.URLs) == 0 {
		return "", fmt.Errorf("no URLs found for chart %s %s at %s", chart.Name, chart.Version, location.Repo)
	}

	return chart.Version, nil
}

func getOCIRepoClient(repoURI string, a Auth) (*remote.Repository, error) {
	r, err := remote.NewRepository(repoURI)
	if err != nil {
		return nil, fmt.Errorf("failed to create OCI client: %w", err)
	}

	authCli := &auth.Client{
		Client: getHTTPClient(a),
		Cache:  auth.NewCache(),
	}
	if a.Username != "" {
		cred := auth.Credential{
			Username: a.Username,
			Password: a.Password,
		}
		authCli.Credential = func(ctx context.Context, s string) (auth.Credential, error) {
			return cred, nil
		}
	}
	r.Client = authCli

	if a.BasicHTTP {
		r.PlainHTTP = true
	}

	return r, nil
}

// ChartURL returns the URL to the helm chart from a helm repo server, by
// inspecting the repo's index.yaml
func ChartURL(ctx context.Context, location fleet.HelmOptions, auth Auth, isHelmOps bool) (string, error) {
	if uri, ok := isOCIChart(location, isHelmOps); ok {
		return uri, nil
	}
	repoURL := location.Repo
	if repoURL == "" {
		return location.Chart, nil
	}

	// Aggregate any concurrent helm repo index retrieval for the same combination of repo URL and auth
	i, err, _ := concurrentIndexFetch.Do(auth.Hash()+repoURL, func() (interface{}, error) {
		return getHelmRepoIndex(ctx, repoURL, auth)
	})
	if err != nil {
		return "", err
	}
	repoIndex := i.(helmRepoIndex)

	chart, err := repoIndex.Get(location.Chart, location.Version)
	if err != nil {
		return "", err
	}

	if len(chart.URLs) == 0 {
		return "", fmt.Errorf("no URLs found for chart %s %s at %s", chart.Name, chart.Version, repoURL)
	}
	return toAbsoluteURLIfNeeded(repoURL, chart.URLs[0])
}

type helmRepoIndex interface {
	Get(chart, version string) (*repov1.ChartVersion, error)
}

// getHelmRepoIndex retrieves and parses the index.yaml from a base URL which can be used to find a specific chart and version
func getHelmRepoIndex(ctx context.Context, repoURL string, auth Auth) (helmRepoIndex, error) {
	indexURL, err := url.JoinPath(repoURL, "index.yaml")
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, err
	}

	if auth.Username != "" && auth.Password != "" {
		request.SetBasicAuth(auth.Username, auth.Password)
	}

	client := getHTTPClient(auth)

	resp, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %q: %w", indexURL, err)
	}
	defer resp.Body.Close()

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to read helm repo from %s, error code: %v", indexURL, resp.StatusCode)
	}

	var index repov1.IndexFile
	if err := yaml.Unmarshal(bytes, &index); err != nil {
		return nil, err
	}
	index.SortEntries()
	return &index, nil
}

// GetOCITag fetches the highest available tag matching version v in repository r.
// Returns an error if the remote repository itself returns an error, for instance if the OCI repository is not found.
// If no error is returned, it is the caller's responsibility to check that the returned tag is non-empty.
func GetOCITag(ctx context.Context, r *remote.Repository, v string) (string, error) {
	constraint, err := semver.NewConstraint(v)
	if err != nil {
		return "", fmt.Errorf("failed to compute version constraint from version %q: %w", v, err)
	}

	availableTags, err := registry.Tags(ctx, r)
	if err != nil {
		var regErr errcode.Error
		if errors.As(err, &regErr) {
			err = regErr

			if regErr.Code == errcode.ErrorCodeNameUnknown {
				return "", fmt.Errorf("repository %q not found in the registry", r.Reference.Repository)
			}
		}

		return "", fmt.Errorf("failed to get available tags for version %q: %w", v, err)
	}

	var tagToResolve string
	var resolvedVersion *semver.Version

	_, err = semver.StrictNewVersion(v)
	isExactVersion := err == nil

	// As per https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-tags, available tags
	// are sorted in lexical order. However, the spec does not specify anything about ascending or descending order.
	for _, tag := range availableTags {
		// check for exact match before trying something more involved.
		if isExactVersion && v == tag {
			tagToResolve = tag
			break
		}

		sv, err := semver.NewVersion(tag)
		if err != nil {
			continue
		}

		if !constraint.Check(sv) {
			continue
		}

		if len(tagToResolve) == 0 || sv.GreaterThan(resolvedVersion) {
			tagToResolve = tag
			resolvedVersion = sv
		}
	}

	return tagToResolve, nil
}

func getHTTPClient(auth Auth) *http.Client {
	return &http.Client{
		Transport: transportForAuth(auth.InsecureSkipVerify, auth.CABundle),
		Timeout:   httpClientTimeout,
	}
}

func transportHash(insecureSkipVerify bool, caBundle []byte) string {
	hash := sha256.New()
	for _, v := range [][]byte{
		caBundle,
		{toByte(insecureSkipVerify)},
	} {
		hash.Write(v)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func transportForAuth(insecureSkipVerify bool, caBundle []byte) http.RoundTripper {
	// We don't need the full hash
	hash := transportHash(insecureSkipVerify, caBundle)

	// Fast path: valid transport already exists
	transportsCacheMutex.RLock()
	rt, ok := transportsCache[hash]
	transportsCacheMutex.RUnlock()
	if ok {
		return rt
	}

	transportsCacheMutex.Lock()
	defer transportsCacheMutex.Unlock()

	// Check again using write lock
	if rt, ok := transportsCache[hash]; ok {
		return rt
	}

	// Create new transport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: insecureSkipVerify, //nolint:gosec
	}
	if caBundle != nil {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(caBundle)

		transport.TLSClientConfig.RootCAs = pool
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}

	transportsCache[hash] = transport
	return transport
}

func isOCIChart(location fleet.HelmOptions, isHelmOps bool) (string, bool) {
	OCIField := location.Chart
	if isHelmOps {
		OCIField = location.Repo
	}

	if strings.HasPrefix(OCIField, ociURLPrefix) {
		return OCIField, true
	}
	return "", false
}

func toAbsoluteURLIfNeeded(baseURL, chartURL string) (string, error) {
	// Check if already absolute
	chartU, err := url.Parse(chartURL)
	if err != nil || chartU.IsAbs() {
		return chartURL, err
	}

	return url.JoinPath(baseURL, chartURL)
}
