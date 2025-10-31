package bundlereader

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/go-getter/v2"
	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/internal/helmupdater"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"helm.sh/helm/v3/pkg/downloader"
	helmgetter "helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
)

// ignoreTree represents a tree of ignored paths (read from .fleetignore files), each node being a directory.
// It provides a means for ignored paths to be propagated down the tree, but not between subdirectories of a same
// directory.
type ignoreTree struct {
	path         string
	ignoredPaths []string
	children     []*ignoreTree
}

// isIgnored checks whether any path within xt matches path, and returns true if so.
func (xt *ignoreTree) isIgnored(path string, info fs.DirEntry) (bool, error) {
	steps := xt.findNode(path, false, nil)

	for _, step := range steps {
		for _, ignoredPath := range step.ignoredPaths {
			if isAllFilesInDirPattern(ignoredPath) {
				// ignores a folder
				if info.IsDir() {
					dirNameInPattern := strings.TrimSuffix(ignoredPath, "/*")
					if dirNameInPattern == filepath.Base(path) {
						return true, nil
					}
				}
			} else {
				toIgnore, err := filepath.Match(ignoredPath, filepath.Base(path))
				if err != nil {
					return false, err
				}

				if toIgnore {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

func isAllFilesInDirPattern(path string) bool {
	match, _ := regexp.MatchString("^.+/\\*", path)
	return match
}

// addNode reads a `.fleetignore` file in dir's root and adds each of its entries to ignored paths for dir.
// Returns an error if a `.fleetignore` file exists for dir but reading it fails.
func (xt *ignoreTree) addNode(dir string) error {
	toIgnore, err := readFleetIgnore(dir)
	if err != nil {
		return fmt.Errorf("read .fleetignore for %s: %v", dir, err)
	}

	if len(toIgnore) == 0 {
		return nil
	}

	steps := xt.findNode(dir, true, nil)
	if steps == nil {
		return fmt.Errorf("ignore tree node not found for path %q", dir)
	}

	destNode := steps[len(steps)-1]
	destNode.ignoredPaths = append(destNode.ignoredPaths, toIgnore...)

	return nil
}

// findNode finds the right node for path, creating that node if needed and if isDir is true.
// Returns a slice representing all relevant nodes in the path to the destination, in order of traversal from the root.
// The last element of that slice is the destination node.
func (xt *ignoreTree) findNode(path string, isDir bool, nodesRoute []*ignoreTree) []*ignoreTree {
	// The path doesn't even belong in the tree. This should never happen.
	if !strings.HasPrefix(path, xt.path) {
		return nil
	}

	nodesRoute = append(nodesRoute, xt)

	if path == xt.path {
		return nodesRoute
	}

	for _, c := range xt.children {
		if steps := c.findNode(path, isDir, nodesRoute); steps != nil {
			crossed := append(nodesRoute, steps...)

			return crossed
		}
	}

	if isDir {
		xt.children = append(xt.children, &ignoreTree{path: path})

		createdChild := xt.children[len(xt.children)-1]

		return append(nodesRoute, createdChild)
	}

	return append(nodesRoute, xt)
}

// readFleetIgnore reads a possible .fleetignore file within path and returns its entries as a slice of strings.
// If no .fleetignore exists, then an empty slice and a nil error are returned.
// If an error happens while opening an existing .fleetignore file, that error is returned along with an empty slice.
func readFleetIgnore(path string) ([]string, error) {
	file, err := os.Open(filepath.Join(path, ".fleetignore"))
	if err != nil {
		// No ignored paths to add if no .fleetignore exists.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)

	var ignored []string

	trailingSpaceRegex := regexp.MustCompile(`([^\\])\s+$`)

	for scanner.Scan() {
		path := scanner.Text()

		// Trim trailing spaces unless escaped.
		path = trailingSpaceRegex.ReplaceAllString(path, "$1")

		// Ignore empty lines and comments (although they should not match any file).
		if path == "" || strings.HasPrefix(path, "#") {
			continue
		}

		ignored = append(ignored, path)
	}

	return ignored, nil
}

func loadDirectory(ctx context.Context, opts loadOpts, dir directory) ([]fleet.BundleResource, error) {
	var resources []fleet.BundleResource

	files, err := GetContent(ctx, dir.base, dir.source, dir.version, dir.auth, opts.disableDepsUpdate, opts.ignoreApplyConfigs)
	if err != nil {
		return nil, err
	}

	for name, data := range files {
		r := fleet.BundleResource{Name: name}
		if opts.compress || !utf8.Valid(data) {
			content, err := content.Base64GZ(data)
			if err != nil {
				return nil, err
			}
			r.Content = content
			r.Encoding = "base64+gz"
		} else {
			r.Content = string(data)
		}
		if dir.prefix != "" {
			r.Name = filepath.Join(dir.prefix, name)
		}
		resources = append(resources, r)
	}

	return resources, nil
}

// GetContent uses go-getter (and Helm for OCI) to read the files from directories and servers.
func GetContent(ctx context.Context, base, source, version string, auth Auth, disableDepsUpdate bool, ignoreApplyConfigs []string) (map[string][]byte, error) {
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

	customGetters := []getter.Getter{}
	for _, g := range getter.Getters {
		// Replace default HTTP(S) getter with our customized one
		if _, ok := g.(*getter.HttpGetter); ok {
			continue
		}
		customGetters = append(customGetters, g)
	}

	httpGetter := newHttpGetter(auth)
	customGetters = append(customGetters, httpGetter)

	client := &getter.Client{
		Getters: customGetters,
	}

	req := &getter.Request{
		Src:     source,
		Dst:     temp,
		Pwd:     base,
		GetMode: getter.ModeDir,
	}

	if auth.CABundle != nil {
		tmpFile, err := os.CreateTemp("", "ca-bundle")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmpFile.Name())
		if _, err := tmpFile.Write(auth.CABundle); err != nil {
			return nil, err
		}
		if err := os.Setenv("GIT_SSL_CAINFO", tmpFile.Name()); err != nil {
			return nil, err
		}
		defer os.Unsetenv("GIT_SSL_CAINFO")
	}

	if _, err := client.Get(ctx, req); err != nil {
		return nil, err
	}

	files := map[string][]byte{}

	// dereference link if possible
	if dest, err := os.Readlink(temp); err == nil {
		temp = dest
	}

	ignoredPaths := ignoreTree{path: temp}

	err = filepath.WalkDir(temp, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		name, err := filepath.Rel(temp, path)
		if err != nil {
			return err
		}

		ignore, err := ignoredPaths.isIgnored(path, info)
		if err != nil {
			return err
		}

		// ignore files containing only fleet apply config
		if slices.Contains(ignoreApplyConfigs, name) {
			return nil
		}

		if info.IsDir() {
			// If the folder is a helm chart and dependency updates are not disabled,
			// try to update possible dependencies.
			if !disableDepsUpdate && helmupdater.ChartYAMLExists(path) {
				if err = helmupdater.UpdateHelmDependencies(path); err != nil {
					return err
				}
			}
			// Skip .fleetignore'd and hidden directories
			if ignore || strings.HasPrefix(filepath.Base(path), ".") {
				return filepath.SkipDir
			}

			return ignoredPaths.addNode(path)
		}

		if ignore {
			return nil
		}

		// Skip hidden files
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
		return nil, fmt.Errorf("failed to read %s relative to %s: %w", orgSource, base, err)
	}

	return files, nil
}

// downloadOCIChart uses Helm to download charts from OCI based registries
func downloadOCIChart(name, version, path string, auth Auth) (string, error) {
	var requiresLogin = auth.Username != "" && auth.Password != ""

	url, err := url.Parse(name)
	if err != nil {
		return "", err
	}

	temp, err := os.MkdirTemp("", "creds")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)

	tmpGetter := newHttpGetter(auth)
	clientOptions := []registry.ClientOption{
		registry.ClientOptCredentialsFile(filepath.Join(temp, "creds.json")),
		registry.ClientOptHTTPClient(tmpGetter.Client),
	}
	if auth.BasicHTTP {
		clientOptions = append(clientOptions, registry.ClientOptPlainHTTP())
	}
	registryClient, err := registry.NewClient(clientOptions...)
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

		err = registryClient.Login(
			addr,
			registry.LoginOptInsecure(auth.InsecureSkipVerify),
			registry.LoginOptBasicAuth(auth.Username, auth.Password),
		)
		if err != nil {
			return "", err
		}
	}

	getterOptions := []helmgetter.Option{}
	if auth.Username != "" && auth.Password != "" {
		getterOptions = append(getterOptions, helmgetter.WithBasicAuth(auth.Username, auth.Password))
	}
	getterOptions = append(getterOptions, helmgetter.WithInsecureSkipVerifyTLS(auth.InsecureSkipVerify))

	c := downloader.ChartDownloader{
		Verify: downloader.VerifyNever,
		Getters: helmgetter.Providers{
			helmgetter.Provider{
				Schemes: []string{registry.OCIScheme},
				New: func(options ...helmgetter.Option) (helmgetter.Getter, error) {
					return helmgetter.NewOCIGetter(helmgetter.WithRegistryClient(registryClient))
				},
			},
		},
		RegistryClient: registryClient,
		Options:        getterOptions,
	}

	saved, _, err := c.DownloadTo(name, version, path)
	if err != nil {
		return "", fmt.Errorf("helm chart download: %w", err)
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

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if auth.CABundle != nil {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(auth.CABundle)
		transport.TLSClientConfig = &tls.Config{
			RootCAs:            pool,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: auth.InsecureSkipVerify, // nolint:gosec
		}
	} else if auth.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: auth.InsecureSkipVerify, // nolint:gosec
		}
	}
	httpGetter.Client.Transport = transport

	return httpGetter
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
