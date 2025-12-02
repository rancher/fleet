package bundlereader

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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

var concurrentIndexFetch singleflight.Group

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

// chartURL returns the URL to the helm chart from a helm repo server, by
// inspecting the repo's index.yaml
func chartURL(ctx context.Context, location fleet.HelmOptions, auth Auth, isHelmOps bool) (string, error) {
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
	client := &http.Client{}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: auth.InsecureSkipVerify, //nolint:gosec
	}
	// This is mainly a throw-away HTTP client and won't be reused for successive bundles processing.
	// DisableKeepAlives will close TCP connections as soon as the HTTP request finishes, preventing idle connections to be left open longer than needed.
	transport.DisableKeepAlives = true

	if auth.CABundle != nil {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(auth.CABundle)

		transport.TLSClientConfig.RootCAs = pool
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}

	client.Transport = transport

	return client
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

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	return u.ResolveReference(chartU).String(), nil
}
