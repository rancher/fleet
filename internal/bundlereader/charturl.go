package bundlereader

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"
)

// ChartVersion returns the version of the helm chart from a helm repo server, by
// inspecting the repo's index.yaml
func ChartVersion(location fleet.HelmOptions, auth Auth) (string, error) {
	if hasOCIURL.MatchString(location.Chart) {
		return location.Version, nil
	}

	if location.Repo == "" {
		return location.Version, nil
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

	return chart.Version, nil
}

// ChartURL returns the URL to the helm chart from a helm repo server, by
// inspecting the repo's index.yaml
func ChartURL(location fleet.HelmOptions, auth Auth) (string, error) {
	if hasOCIURL.MatchString(location.Chart) {
		return location.Chart, nil
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
	client := &http.Client{}
	if auth.CABundle != nil {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(auth.CABundle)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{
			RootCAs:            pool,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: auth.InsecureSkipVerify, // nolint:gosec
		}
		client.Transport = transport
	} else {
		if auth.InsecureSkipVerify {
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.TLSClientConfig = &tls.Config{
				InsecureSkipVerify: auth.InsecureSkipVerify, // nolint:gosec
			}
			client.Transport = transport
		}
	}

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
		return nil, fmt.Errorf("failed to read helm repo from %s, error code: %v, response body: %s", location.Repo+"index.yaml", resp.StatusCode, bytes)
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
