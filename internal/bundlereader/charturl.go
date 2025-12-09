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
	"helm.sh/helm/v3/pkg/repo"
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
	transportsCache      = map[string]http.RoundTripper{}
	transportsCacheMutex sync.RWMutex
)

// ChartVersion returns the version of the helm chart from a helm repo server, by
// inspecting the repo's index.yaml
func ChartVersion(location fleet.HelmOptions, a Auth) (string, error) {
	if hasOCIURL.MatchString(location.Repo) {
		repo := strings.TrimPrefix(location.Repo, "oci://")

		r, err := remote.NewRepository(repo)
		if err != nil {
			return "", fmt.Errorf("failed to create OCI client: %w", err)
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

		tag, err := GetOCITag(r, location.Version)

		if len(tag) == 0 || err != nil {
			return "", fmt.Errorf(
				"could not find tag matching constraint %q in registry %s: %v",
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

	if !strings.HasSuffix(location.Repo, "/") {
		location.Repo = location.Repo + "/"
	}

	chart, err := getHelmChartVersion(location, a)
	if err != nil {
		return "", err
	}

	if len(chart.URLs) == 0 {
		return "", fmt.Errorf("no URLs found for chart %s %s at %s", chart.Name, chart.Version, location.Repo)
	}

	return chart.Version, nil
}

// chartURL returns the URL to the helm chart from a helm repo server, by
// inspecting the repo's index.yaml
func chartURL(location fleet.HelmOptions, auth Auth, isHelmOps bool) (string, error) {
	OCIField := location.Chart
	if isHelmOps {
		OCIField = location.Repo
	}

	if hasOCIURL.MatchString(OCIField) {
		return OCIField, nil
	}

	if location.Repo == "" {
		return location.Chart, nil
	}

	if !strings.HasSuffix(location.Repo, "/") {
		location.Repo = location.Repo + "/"
	}

	chart, err := getHelmChartVersion(location, auth)
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
func getHelmChartVersion(location fleet.HelmOptions, auth Auth) (*repo.ChartVersion, error) {
	request, err := http.NewRequest("GET", location.Repo+"index.yaml", nil)
	if err != nil {
		return nil, err
	}

	if auth.Username != "" && auth.Password != "" {
		request.SetBasicAuth(auth.Username, auth.Password)
	}

	client := getHTTPClient(auth)

	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to read helm repo from %s, error code: %v", location.Repo+"index.yaml", resp.StatusCode)
	}

	repo := &repo.IndexFile{}
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
func GetOCITag(r *remote.Repository, v string) (string, error) {
	constraint, err := semver.NewConstraint(v)
	if err != nil {
		return "", fmt.Errorf("failed to compute version constraint from version %q: %w", v, err)
	}

	availableTags, err := registry.Tags(context.TODO(), r)
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
