package bundlereader

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/go-getter"
	"github.com/pkg/errors"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/internal/pkg/content"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/downloader"
	helmgetter "helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
)

func loadDirectory(ctx context.Context, compress bool, prefix, base, source, version string, auth Auth) ([]fleet.BundleResource, error) {
	var resources []fleet.BundleResource

	files, err := getContent(ctx, base, source, version, auth)
	if err != nil {
		return nil, err
	}

	for name, data := range files {
		r := fleet.BundleResource{Name: name}
		if compress || !utf8.Valid(data) {
			content, err := content.Base64GZ(data)
			if err != nil {
				return nil, err
			}
			r.Content = content
			r.Encoding = "base64+gz"
		} else {
			r.Content = string(data)
		}
		if prefix != "" {
			r.Name = filepath.Join(prefix, name)
		}
		resources = append(resources, r)
	}

	return resources, nil
}

// getContent uses go-getter (and helm for oci) to read the files from directories and servers
func getContent(ctx context.Context, base, source, version string, auth Auth) (map[string][]byte, error) {
	temp, err := os.MkdirTemp("", "fleet")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)

	orgSource := source

	// go-getter does not support downloading OCI registry based files yet
	// until this is implemented we use Helm to download charts from OCI based registries
	// and provide the downloaded file to go-getter locally
	if hasOCIURL.MatchString(source) {
		source, err = downloadOCIChart(source, version, temp, auth)
		if err != nil {
			return nil, err
		}
	}

	temp = filepath.Join(temp, "content")

	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}

	if auth.SSHPrivateKey != nil {
		if !strings.ContainsAny(source, "?") {
			source += "?"
		} else {
			source += "&"
		}
		source += fmt.Sprintf("sshkey=%s", base64.StdEncoding.EncodeToString(auth.SSHPrivateKey))
	}

	// copy getter.Getters before changing
	getters := map[string]getter.Getter{}
	for k, v := range getter.Getters {
		getters[k] = v
	}

	httpGetter := newHttpGetter(auth)
	getters["http"] = httpGetter
	getters["https"] = httpGetter

	c := getter.Client{
		Ctx:     ctx,
		Src:     source,
		Dst:     temp,
		Pwd:     base,
		Mode:    getter.ClientModeDir,
		Getters: getters,
		// TODO: why doesn't this work anymore
		//ProgressListener: progress,
	}

	if err := c.Get(); err != nil {
		return nil, err
	}

	files := map[string][]byte{}

	// dereference link if possible
	if dest, err := os.Readlink(temp); err == nil {
		temp = dest
	}

	err = filepath.Walk(temp, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if strings.HasPrefix(filepath.Base(path), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		name, err := filepath.Rel(temp, path)
		if err != nil {
			return err
		}

		if strings.HasPrefix(filepath.Base(name), ".") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		files[name] = content
		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s relative to %s", orgSource, base)
	}

	return files, nil
}

// downloadOCIChart uses Helm to download charts from OCI based registries
func downloadOCIChart(name, version, path string, auth Auth) (string, error) {
	var registryClient *registry.Client
	var requiresLogin bool = auth.Username != "" && auth.Password != ""

	url, err := url.Parse(name)
	if err != nil {
		return "", err
	}

	// Helm does not support direct authentication for private OCI registries when a chart is downloaded
	// so it is necessary to login before via Helm which stores the registry token in a configuration
	// file on the system
	addr := url.Hostname()
	if requiresLogin {
		if port := url.Port(); port != "" {
			addr = fmt.Sprintf("%s:%s", addr, port)
		}

		registryClient, err = registry.NewClient()
		if err != nil {
			return "", err
		}

		err = registryClient.Login(
			addr,
			registry.LoginOptInsecure(false),
			registry.LoginOptBasicAuth(auth.Username, auth.Password),
		)
		if err != nil {
			return "", err
		}
	}

	c := downloader.ChartDownloader{
		Verify:         downloader.VerifyNever,
		Getters:        helmgetter.All(&cli.EnvSettings{}),
		RegistryClient: registryClient,
	}

	saved, _, err := c.DownloadTo(name, version, path)
	if err != nil {
		return "", fmt.Errorf("Helm chart download: %v", err)
	}

	// Logout to remove the token configuration file from the system again
	if requiresLogin {
		err = registryClient.Logout(addr)
		if err != nil {
			return "", err
		}
	}

	return saved, nil
}

func newHttpGetter(auth Auth) *getter.HttpGetter {
	httpGetter := &getter.HttpGetter{
		Client: &http.Client{},
	}

	if auth.Username != "" && auth.Password != "" {
		header := http.Header{}
		header.Add("Authorization", "Basic "+basicAuth(auth.Username, auth.Password))
		httpGetter.Header = header
	}
	if auth.CABundle != nil {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(auth.CABundle)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}
		httpGetter.Client.Transport = transport
	}
	return httpGetter
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
