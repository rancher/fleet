package bundlereader

import (
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/rancher/fleet/internal/helmversion"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetgit "github.com/rancher/fleet/pkg/git"
	"golang.org/x/sync/singleflight"
	repov1 "helm.sh/helm/v4/pkg/repo/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	// Users may spell build metadata the way a registry tag does, with an
	// underscore instead of a plus sign.
	location.Version = helmversion.Normalize(location.Version)

	if repoURI, ok := strings.CutPrefix(location.Repo, ociURLPrefix); ok {
		client, err := getOCIRepoClient(repoURI, a)
		if err != nil {
			return "", err
		}

		tag, err := GetOCITag(ctx, client, location.Version)
		if err != nil {
			return "", fmt.Errorf("registry %s: %w", location.Repo, err)
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
func ChartURL(ctx context.Context, location fleet.HelmOptions, auth Auth) (string, error) {
	if uri, ok := isOCIChart(location); ok {
		return uri, nil
	}
	repoURL := location.Repo
	if repoURL == "" {
		return location.Chart, nil
	}

	// Aggregate any concurrent helm repo index retrieval for the same combination of repo URL and auth
	i, err, _ := concurrentIndexFetch.Do(auth.Hash()+repoURL, func() (any, error) {
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

// NoMatchingTagError reports that a registry listed the tags of a repository and
// none of them holds the version asked for. It tells that apart from the tags
// being out of reach, which leaves the matching tag unknown rather than absent.
type NoMatchingTagError struct {
	Version string
}

func (e *NoMatchingTagError) Error() string {
	return fmt.Sprintf("no tag matching version %q found", e.Version)
}

// GetOCITag fetches the tag matching version v in repository r, and returns it
// spelled as semver, so that Helm can pull it.
//
// A fully specified version only matches tags holding that version. Spelling
// out build metadata pins a single build, so that only the tag publishing it
// matches; leaving the metadata out matches every build of the version, and the
// highest one wins, as a registry following the convention publishes the chart
// under its build rather than under the bare version. If v is a constraint, the
// highest matching tag is returned, build metadata breaking ties between tags
// semver considers equal. An empty v means any version, as it does for Helm.
//
// Returns an error if no tag matches, or if the remote repository itself returns
// one, for instance if the OCI repository is not found.
func GetOCITag(ctx context.Context, r *remote.Repository, v string) (string, error) {
	wanted, isExact := helmversion.ParseExact(v)

	// A fully specified version which spells out build metadata asks for that one
	// build; one which does not leaves the build open.
	pinsBuild := isExact && wanted.Metadata() != ""

	var constraint *semver.Constraints
	if !isExact {
		var err error
		if constraint, err = semver.NewConstraint(cmp.Or(v, "*")); err != nil {
			return "", fmt.Errorf("failed to compute version constraint from version %q: %w", v, err)
		}
	}

	availableTags, err := registry.Tags(ctx, r)
	if err != nil {
		if regErr, ok := errors.AsType[errcode.Error](err); ok {
			err = regErr

			if regErr.Code == errcode.ErrorCodeNameUnknown {
				return "", fmt.Errorf("repository %q not found in the registry", r.Reference.Repository)
			}
		}

		return "", fmt.Errorf("failed to get available tags for version %q: %w", v, err)
	}

	var tagToResolve string
	var resolvedVersion *semver.Version

	// As per https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-tags, available tags
	// are sorted in lexical order. However, the spec does not specify anything about ascending or descending order.
	for _, tag := range availableTags {
		// A tag holds an underscore where the version it publishes has build
		// metadata, as a registry does not accept a plus sign in a tag.
		version := helmversion.FromTag(tag)

		// Tags which are not versions, and tags holding a version semver only
		// accepts loosely, are no candidates: the latter cannot be deployed, so
		// resolving to one would only trade this error for a later one naming a
		// version the user never asked for.
		sv, err := helmversion.Parse(version)
		if err != nil {
			continue
		}

		if isExact {
			// Semver comparison ignores build metadata, so this keeps every build of
			// the wanted version.
			if !sv.Equal(wanted) {
				continue
			}

			// Build metadata is what tells two tags for the same version apart, so a
			// version asking for one build has to match it as well.
			if pinsBuild && !helmversion.Equal(sv, wanted) {
				continue
			}
		} else if !constraint.Check(sv) {
			continue
		}

		if resolvedVersion != nil {
			c := helmversion.Compare(sv, resolvedVersion)

			// Two tags whose versions are equal, build metadata included, are two
			// spellings of the same version, such as 1.2.3 and v1.2.3. Settle on one
			// of them, as the distribution spec makes no promise about the order in
			// which a registry lists its tags.
			if c < 0 || (c == 0 && version >= tagToResolve) {
				continue
			}
		}

		tagToResolve, resolvedVersion = version, sv
	}

	if resolvedVersion == nil {
		return "", &NoMatchingTagError{Version: v}
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

	// Write a length prefix for every field to avoid collisions
	lenBuf := make([]byte, 8)
	writeField := func(data []byte) {
		binary.LittleEndian.PutUint64(lenBuf, uint64(len(data)))
		hash.Write(lenBuf)
		hash.Write(data)
	}

	for _, v := range [][]byte{ // values to hash
		caBundle,
		{toByte(insecureSkipVerify)},
	} {
		writeField(v)
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func transportForAuth(insecureSkipVerify bool, caBundle []byte) http.RoundTripper {
	caBundle = append([]byte(nil), caBundle...) // defensive copy
	if proxyCAPEM, ok := os.LookupEnv(fleetgit.ProxyCABundleEnvVar); ok && proxyCAPEM != "" {
		proxyBytes := []byte(proxyCAPEM)
		tmpPool := x509.NewCertPool()
		if !tmpPool.AppendCertsFromPEM(proxyBytes) {
			log.Log.Info(fleetgit.ProxyCABundleEnvVar + " is set but contains no valid PEM certificates; ignoring proxy CA bundle")
		} else {
			caBundle = append(caBundle, '\n')
			caBundle = append(caBundle, proxyBytes...)
		}
	}

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
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// Another component has replaced the global default transport.
		// Construct a transport that preserves the standard proxy and timeout
		// defaults so runtime behaviour stays close to the stdlib baseline.
		baseTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	transport := baseTransport.Clone()
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

func isOCIChart(location fleet.HelmOptions) (string, bool) {
	if strings.HasPrefix(location.Repo, ociURLPrefix) {
		return location.Repo, true
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
