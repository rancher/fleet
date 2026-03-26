package bundlereader

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"helm.sh/helm/v4/pkg/downloader"
	helmgetter "helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/registry"

	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/internal/helmupdater"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
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
		return fmt.Errorf("read .fleetignore for %s: %w", dir, err)
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
			return append(nodesRoute, steps...)
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
				return nil, fmt.Errorf("decoding compressed base64 data: %w", err)
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

// GetContent uses fetchToDir (and Helm for OCI) to read the files from directories and servers.
func GetContent(ctx context.Context, base, source, version string, auth Auth, disableDepsUpdate bool, ignoreApplyConfigs []string) (map[string][]byte, error) {
	temp, err := os.MkdirTemp("", "fleet")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)

	orgSource := source

	// OCI registries are handled via Helm; the downloaded chart is then read locally.
	if strings.HasPrefix(source, ociURLPrefix) {
		source, err = downloadOCIChart(source, version, temp, auth)
		if err != nil {
			return nil, fmt.Errorf("downloading OCI chart from %q: %w", redactURL(orgSource), err)
		}
	}

	temp = filepath.Join(temp, "content")

	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}

	if err := fetchToDir(ctx, temp, source, base, auth); err != nil {
		return nil, fmt.Errorf("retrieving file from %q: %w", redactURL(orgSource), err)
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
					return fmt.Errorf("updating helm dependencies: %w", err)
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

		content, err := os.ReadFile(path) //nolint:gosec // G122: path is from WalkDir over a go-getter controlled temp directory
		if err != nil {
			return err
		}

		files[name] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read %s relative to %s: %w", redactURL(orgSource), base, err)
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

	clientOptions := []registry.ClientOption{
		registry.ClientOptCredentialsFile(filepath.Join(temp, "creds.json")),
		registry.ClientOptHTTPClient(getHTTPClient(auth)),
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
		Verify:       downloader.VerifyNever,
		ContentCache: path, // Required in Helm v4
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

// safeJoinSubDir joins base and sub, returning an error if sub is absolute or
// escapes base (e.g., due to ".." components).
func safeJoinSubDir(base, sub string) (string, error) {
	cleanSub := filepath.Clean(filepath.FromSlash(sub))
	if filepath.IsAbs(cleanSub) {
		return "", fmt.Errorf("subdir must be relative, got %q", sub)
	}
	if cleanSub == "." {
		return base, nil
	}
	joined := filepath.Join(base, cleanSub)
	rel, err := filepath.Rel(base, joined)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("subdir %q escapes base directory", sub)
	}
	return joined, nil
}

// fetchToDir resolves source (relative to pwd), downloads or copies it, and
// places the resulting files into dst.
func fetchToDir(ctx context.Context, dst, source, pwd string, auth Auth) error {
	si, err := parseSource(source, pwd)
	if err != nil {
		return err
	}

	// If there's a subdirectory, download into a temp directory then move the
	// subdirectory into dst.
	if si.subDir != "" {
		// For local directory sources, compose the subdir path directly without
		// a temp directory. fetchScheme would try os.Symlink(rawURL, td) which
		// fails because td already exists as a directory from os.MkdirTemp.
		if si.scheme == "local" {
			fi, err := os.Stat(si.rawURL)
			if err != nil {
				return err
			}
			if fi.IsDir() {
				src, err := safeJoinSubDir(si.rawURL, si.subDir)
				if err != nil {
					return fmt.Errorf("invalid subdir %q: %w", si.subDir, err)
				}
				if err := os.MkdirAll(dst, 0750); err != nil {
					return err
				}
				return copyDir(src, dst)
			}
			// Local file (e.g. an archive): fall through to the temp-dir path.
		}

		td, err := os.MkdirTemp("", "fleet-subdir")
		if err != nil {
			return err
		}
		defer os.RemoveAll(td)

		if err := fetchScheme(ctx, td, si.scheme, si.rawURL, auth); err != nil {
			return err
		}

		src, err := safeJoinSubDir(td, si.subDir)
		if err != nil {
			return fmt.Errorf("invalid subdir %q: %w", si.subDir, err)
		}
		if err := os.MkdirAll(dst, 0750); err != nil {
			return err
		}
		if err := copyDir(src, dst); err != nil {
			return fmt.Errorf("copying subdir %q: %w", si.subDir, err)
		}
		return nil
	}

	return fetchScheme(ctx, dst, si.scheme, si.rawURL, auth)
}

func fetchScheme(ctx context.Context, dst, scheme, rawURL string, auth Auth) error {
	switch scheme {
	case "git":
		return gitDownload(ctx, dst, rawURL, auth)
	case "http":
		return httpDownload(ctx, dst, rawURL, auth)
	case "local":
		fi, err := os.Stat(rawURL)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			// Mirror go-getter's FileGetter behaviour: create a symlink at dst
			// pointing to rawURL so that WalkDir and helmupdater operate on the
			// original directory (with writes going back to the source).
			// GetContent's os.Readlink call then resolves dst to the source path.
			return os.Symlink(rawURL, dst)
		}
		// A single file (e.g. an OCI-downloaded .tgz chart) — extract into dst.
		if err := os.MkdirAll(dst, 0750); err != nil {
			return err
		}
		f, err := os.Open(rawURL)
		if err != nil {
			return err
		}
		defer f.Close()
		return extractResponse(dst, filepath.Base(rawURL), "", f)
	default:
		return fmt.Errorf("unsupported scheme %q", scheme)
	}
}

// copyDir recursively copies the content of src into dst.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0750)
		}
		// Reject symlinks: following them could copy data from outside the
		// cloned tree if the repository contains malicious symlinks.
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("copyDir: symlink not supported: %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		//nolint:gosec // G304: path is derived from a go-git controlled temp directory
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer dstFile.Close()
		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}
