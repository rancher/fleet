package bundle

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/hashicorp/go-getter"
	"github.com/pkg/errors"
	"github.com/rancher/fleet/modules/cli/pkg/progress"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/content"
	"github.com/rancher/wrangler/pkg/data"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"
)

func readResources(ctx context.Context, spec *fleet.BundleSpec, compress bool, base string, auth Auth) ([]fleet.BundleResource, error) {
	var directories []directory

	directories, err := addDirectory(directories, base, ".", ".")
	if err != nil {
		return nil, err
	}

	var chartDirs []*fleet.HelmOptions

	if spec.Helm != nil && spec.Helm.Chart != "" {
		if err := parseValueFiles(base, spec.Helm); err != nil {
			return nil, err
		}
		chartDirs = append(chartDirs, spec.Helm)
	}

	for _, target := range spec.Targets {
		if target.Helm != nil {
			err := parseValueFiles(base, target.Helm)
			if err != nil {
				return nil, err
			}
			if target.Helm.Chart != "" {
				chartDirs = append(chartDirs, target.Helm)
			}
		}
	}

	directories, err = addCharts(directories, base, chartDirs, auth)
	if err != nil {
		return nil, err
	}

	resources, err := readDirectories(ctx, compress, directories...)
	if err != nil {
		return nil, err
	}

	var result []fleet.BundleResource
	for _, resources := range resources {
		result = append(result, resources...)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func ChartPath(helm *fleet.HelmOptions) string {
	if helm == nil {
		return "none"
	}
	return fmt.Sprintf(".chart/%x", sha256.Sum256([]byte(helm.Chart + ":" + helm.Repo + ":" + helm.Version)[:]))
}

type Auth struct {
	Username      string
	Password      string
	CABundle      []byte
	SSHPrivateKey []byte
}

func chartURL(location *fleet.HelmOptions, auth Auth) (string, error) {
	if location.Repo == "" {
		return location.Chart, nil
	}

	if !strings.HasSuffix(location.Repo, "/") {
		location.Repo = location.Repo + "/"
	}

	request, err := http.NewRequest("GET", location.Repo+"index.yaml", nil)
	if err != nil {
		return "", err
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
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}
		client.Transport = transport
	}

	resp, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to read helm repo from %s, error code: %v, response body: %s", location.Repo+"index.yaml", resp.StatusCode, bytes)
	}

	repo := &repo.IndexFile{}
	if err := yaml.Unmarshal(bytes, repo); err != nil {
		return "", err
	}

	repo.SortEntries()

	chart, err := repo.Get(location.Chart, location.Version)
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

func addCharts(directories []directory, base string, charts []*fleet.HelmOptions, auth Auth) ([]directory, error) {
	for _, chart := range charts {
		if _, err := os.Stat(filepath.Join(base, chart.Chart)); os.IsNotExist(err) || chart.Repo != "" {
			chartURL, err := chartURL(chart, auth)
			if err != nil {
				return nil, err
			}

			directories = append(directories, directory{
				prefix: ChartPath(chart),
				base:   base,
				path:   chartURL,
				key:    ChartPath(chart),
				auth:   auth,
			})
		}
	}
	return directories, nil
}

func addDirectory(directories []directory, base, customDir, defaultDir string) ([]directory, error) {
	if customDir == "" {
		if _, err := os.Stat(filepath.Join(base, defaultDir)); os.IsNotExist(err) {
			return directories, nil
		} else if err != nil {
			return directories, err
		}
		customDir = defaultDir
	}

	return append(directories, directory{
		prefix: defaultDir,
		base:   base,
		path:   customDir,
		key:    defaultDir,
	}), nil
}

type directory struct {
	prefix string
	base   string
	path   string
	key    string
	auth   Auth
}

func readDirectories(ctx context.Context, compress bool, directories ...directory) (map[string][]fleet.BundleResource, error) {
	var (
		sem    = semaphore.NewWeighted(4)
		result = map[string][]fleet.BundleResource{}
		l      = sync.Mutex{}
		p      = progress.NewProgress()
	)
	defer p.Close()

	eg, ctx := errgroup.WithContext(ctx)

	for _, dir := range directories {
		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		dir := dir
		eg.Go(func() error {
			defer sem.Release(1)
			resources, err := readDirectory(ctx, compress, dir.prefix, dir.base, dir.path, dir.auth)
			if err != nil {
				return err
			}

			key := dir.key
			if key == "" {
				key = dir.path
			}

			l.Lock()
			result[key] = resources
			l.Unlock()
			return nil
		})
	}

	return result, eg.Wait()
}

func readDirectory(ctx context.Context, compress bool, prefix, base, name string, auth Auth) ([]fleet.BundleResource, error) {
	var resources []fleet.BundleResource

	files, err := readContent(ctx, base, name, auth)
	if err != nil {
		return nil, err
	}

	for k := range files {
		resources = append(resources, fleet.BundleResource{
			Name: k,
		})
	}

	for i, resource := range resources {
		data := files[resource.Name]
		if compress || !utf8.Valid(data) {
			content, err := content.Base64GZ(files[resource.Name])
			if err != nil {
				return nil, err
			}
			resources[i].Content = content
			resources[i].Encoding = "base64+gz"
		} else {
			resources[i].Content = string(data)
		}
		if prefix != "" {
			resources[i].Name = filepath.Join(prefix, resources[i].Name)
		}
	}

	return resources, nil
}

func readContent(ctx context.Context, base, name string, auth Auth) (map[string][]byte, error) {
	temp, err := os.MkdirTemp("", "fleet")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)

	temp = filepath.Join(temp, "content")

	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}

	// copy getter.Getters before changing
	getters := map[string]getter.Getter{}
	for k, v := range getter.Getters {
		getters[k] = v
	}
	src := name

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
	if auth.SSHPrivateKey != nil {
		if !strings.ContainsAny(src, "?") {
			src += "?"
		} else {
			src += "&"
		}
		src += fmt.Sprintf("sshkey=%s", base64.StdEncoding.EncodeToString(auth.SSHPrivateKey))
	}
	getters["http"] = httpGetter
	getters["https"] = httpGetter

	c := getter.Client{
		Ctx:     ctx,
		Src:     src,
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
		return nil, errors.Wrapf(err, "failed to read %s relative to %s", name, base)
	}

	return files, nil
}

func parseValueFiles(base string, chart *fleet.HelmOptions) (err error) {
	if len(chart.ValuesFiles) != 0 {
		valuesMap, err := generateValues(base, chart)
		if err != nil {
			return err
		}

		if len(valuesMap.Data) != 0 {
			chart.Values = valuesMap
		}
	}

	return nil
}

func generateValues(base string, chart *fleet.HelmOptions) (valuesMap *fleet.GenericMap, err error) {
	valuesMap = &fleet.GenericMap{}
	if chart.Values != nil {
		valuesMap = chart.Values
	}
	for _, value := range chart.ValuesFiles {
		valuesByte, err := os.ReadFile(base + "/" + value)
		if err != nil {
			return nil, err
		}
		tmpDataOpt := &fleet.GenericMap{}
		err = yaml.Unmarshal(valuesByte, tmpDataOpt)
		if err != nil {
			return nil, err
		}
		valuesMap = mergeGenericMap(valuesMap, tmpDataOpt)
	}

	return valuesMap, nil
}

func mergeGenericMap(first, second *fleet.GenericMap) *fleet.GenericMap {
	result := &fleet.GenericMap{Data: make(map[string]interface{})}
	result.Data = data.MergeMaps(first.Data, second.Data)
	return result
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
