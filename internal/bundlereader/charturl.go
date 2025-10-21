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
	repov1 "helm.sh/helm/v4/pkg/repo/v1"

	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
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

	if location.Repo == "" {
		return location.Version, nil
	}

	chart, err := getHelmChartVersion(ctx, location, a)
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
	OCIField := location.Chart
	if isHelmOps {
		OCIField = location.Repo
	}

	if strings.HasPrefix(OCIField, ociURLPrefix) {
		return OCIField, nil
	}

	if location.Repo == "" {
		return location.Chart, nil
	}

	chart, err := getHelmChartVersion(ctx, location, auth)
	if err != nil {
		return "", err
	}

	if len(chart.URLs) == 0 {
		return "", fmt.Errorf("no URLs found for chart %s %s at %s", chart.Name, chart.Version, location.Repo)
	}

	chartURL, err := url.Parse(chart.URLs[0])
	if err != nil {
		return "", err
	}

	if chartURL.IsAbs() {
		return chart.URLs[0], nil
	}

	repoURL, err := url.Parse(location.Repo)
	if err != nil {
		return "", err
	}

	return repoURL.ResolveReference(chartURL).String(), nil
}

// getHelmChartVersion returns the ChartVersion struct with the information to the given location
// using the given authentication configuration
func getHelmChartVersion(ctx context.Context, location fleet.HelmOptions, auth Auth) (*repov1.ChartVersion, error) {
	indexURL, err := url.JoinPath(location.Repo, "index.yaml")
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

	repo := &repov1.IndexFile{}
	if err := yaml.Unmarshal(bytes, repo); err != nil {
		return nil, err
	}

	repo.SortEntries()

	chart, err := repo.Get(location.Chart, location.Version)
	if err != nil {
		return nil, err
	}

	return chart, nil
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
