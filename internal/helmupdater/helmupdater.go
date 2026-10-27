package helmupdater

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	v2chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/registry"
	orasauth "oras.land/oras-go/v2/registry/remote/auth"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ChartYaml = "Chart.yaml"
)

// RegistryAuth carries the Helm credentials a bundle provides for the
// dependencies of a chart stored in it.
type RegistryAuth struct {
	Username           string
	Password           string
	InsecureSkipVerify bool
	PlainHTTP          bool
	// HTTPClient carries the transport settings used to reach the registries,
	// such as a CA bundle. nil means http.DefaultClient.
	HTTPClient *http.Client
	// Applies reports whether the credentials may be sent to the registry a
	// dependency repository URL points at.
	Applies func(repoURL string) (bool, error)
}

func (a *RegistryAuth) hasCredentials() bool {
	return a != nil && a.Username != "" && a.Password != "" && a.Applies != nil
}

// ChartYAMLExists checks if the provided path is a directory containing a `Chart.yaml` file.
// Returns true if it does, false otherwise or if an error happens when checking `<path>/Chart.yaml`.
func ChartYAMLExists(path string) bool {
	chartPath := filepath.Join(path, ChartYaml)
	if _, err := os.Stat(chartPath); err != nil {
		return false
	}
	return true
}

// UpdateHelmDependencies checks if the helm chart located at the given directory has unmet dependencies and, if so, updates them.
// auth, when not nil, provides the credentials to log in to the OCI registries of the dependencies it applies to.
func UpdateHelmDependencies(path string, auth *RegistryAuth) error {
	// load the chart to check if there are unmet dependencies first
	chartRequested, err := loader.Load(path)
	if err != nil {
		return err
	}

	req := chartRequested.Metadata.Dependencies
	if req == nil {
		return nil
	}
	// Convert []*v2.Dependency to []chart.Dependency
	deps := make([]chart.Dependency, len(req))
	for i, d := range req {
		deps[i] = d
	}
	if err := action.CheckDependencies(chartRequested, deps); err == nil {
		return nil
	}

	hosts, err := ociHostsNeedingAuth(chartRequested.Name(), req, auth)
	if err != nil {
		return err
	}

	settings := cli.New()
	clientOptions := []registry.ClientOption{
		registry.ClientOptDebug(settings.Debug),
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(os.Stderr),
	}
	if len(hosts) == 0 {
		clientOptions = append(clientOptions, registry.ClientOptCredentialsFile(settings.RegistryConfig))
	} else {
		// Log in through a private credentials file, as downloading an OCI
		// chart does, so the registry token never lands in the system's Helm
		// configuration.
		temp, err := os.MkdirTemp("", "creds")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)

		httpClient := auth.HTTPClient
		if httpClient == nil {
			// Helm's login options read the TLS configuration of the client's
			// transport, so the client needs a real one.
			httpClient = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
		}
		clientOptions = append(clientOptions,
			registry.ClientOptCredentialsFile(filepath.Join(temp, "creds.json")),
			registry.ClientOptHTTPClient(httpClient),
			registry.ClientOptAuthorizer(orasauth.Client{Client: httpClient}),
		)
		if auth.PlainHTTP {
			clientOptions = append(clientOptions, registry.ClientOptPlainHTTP())
		}
	}
	registryClient, err := registry.NewClient(clientOptions...)
	if err != nil {
		return err
	}

	for _, host := range hosts {
		err := registryClient.Login(
			host,
			registry.LoginOptInsecure(auth.InsecureSkipVerify),
			registry.LoginOptPlainText(auth.PlainHTTP),
			registry.LoginOptBasicAuth(auth.Username, auth.Password),
		)
		if err != nil {
			return fmt.Errorf("logging in to %s for the dependencies of chart %s: %w", host, chartRequested.Name(), err)
		}
		defer func(host string) {
			_ = registryClient.Logout(host)
		}(host)
	}

	man := &downloader.Manager{
		Out:              os.Stdout,
		ChartPath:        path,
		Keyring:          "",
		SkipUpdate:       false,
		Getters:          getter.All(settings),
		RepositoryConfig: settings.RegistryConfig,
		RepositoryCache:  settings.RepositoryCache,
		ContentCache:     settings.ContentCache,
		Debug:            settings.Debug,
		RegistryClient:   registryClient,
	}
	return man.Update()
}

// ociHostsNeedingAuth returns the registries the credentials are sent to,
// each once, in the order of the dependencies. A login is per registry, not
// per repository, so a registry is only returned when the credentials apply
// to every OCI dependency hosted on it; otherwise the login would also cover
// the dependencies helmRepoURLRegex excludes.
func ociHostsNeedingAuth(chartName string, deps []*v2chart.Dependency, auth *RegistryAuth) ([]string, error) {
	if !auth.hasCredentials() {
		return nil, nil
	}

	var hosts []string
	excluded := map[string]bool{}
	for _, dep := range deps {
		if !registry.IsOCI(dep.Repository) {
			continue
		}
		u, err := url.Parse(dep.Repository)
		if err != nil {
			return nil, fmt.Errorf("parsing the repository of dependency %s of chart %s: %w", dep.Name, chartName, err)
		}
		ok, err := auth.Applies(dep.Repository)
		if err != nil {
			return nil, fmt.Errorf("checking whether Helm credentials apply to dependency %s of chart %s: %w", dep.Name, chartName, err)
		}
		if !ok {
			log.Log.Info(fmt.Sprintf(
				"helmRepoURLRegex does not match the repository %q of dependency %s of chart %s: Helm credentials will not be forwarded to %s",
				dep.Repository, dep.Name, chartName, u.Host))
			excluded[u.Host] = true
			continue
		}
		if _, known := excluded[u.Host]; !known {
			excluded[u.Host] = false
			hosts = append(hosts, u.Host)
		}
	}

	allowed := hosts[:0]
	for _, host := range hosts {
		if excluded[host] {
			log.Log.Info(fmt.Sprintf(
				"helmRepoURLRegex matches only some of the dependencies of chart %s hosted on %s: Helm credentials will not be forwarded to that registry",
				chartName, host))
			continue
		}
		allowed = append(allowed, host)
	}
	return allowed, nil
}
