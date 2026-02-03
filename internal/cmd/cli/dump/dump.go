package dump

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/rancher/fleet/internal/config"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
)

type Options struct {
	FetchLimit          int64
	Namespace           string
	AllNamespaces       bool
	WithSecrets         bool
	WithSecretsMetadata bool
	WithContent         bool
	WithContentMetadata bool
}

func Create(ctx context.Context, cfg *rest.Config, path string, opt Options) error {
	c, err := createClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	d, err := createDynamicClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create dynamic Kubernetes client: %w", err)
	}

	// Use filtered version when namespace filtering is active
	if !opt.AllNamespaces && opt.Namespace != "" {
		return CreateWithClientsFiltered(ctx, cfg, d, c, path, opt)
	}

	return CreateWithClients(ctx, cfg, d, c, path, opt)
}

func CreateWithClients(ctx context.Context, cfg *rest.Config, d dynamic.Interface, c client.Client, path string, opt Options) error {
	logger := log.FromContext(ctx).WithName("fleet-dump")

	tgz, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}

	gz := gzip.NewWriter(tgz)
	w := tar.NewWriter(gz)

	types := []string{
		"bundles",
		"bundledeployments",
		"bundlenamespacemappings",
		"clusters",
		"clustergroups",
		"gitrepos",
		"gitreporestrictions",
		"helmops",
	}

	for _, t := range types {
		if err := addObjectsToArchive(ctx, d, logger, "fleet.cattle.io", "v1alpha1", t, w, opt); err != nil {
			return fmt.Errorf("failed to add %s to archive: %w", t, err)
		}
	}

	if opt.WithContent || opt.WithContentMetadata {
		// If both full content and metadata-only are requested, prefer full content
		contentMetadataOnly := opt.WithContentMetadata && !opt.WithContent
		if err := addContentsToArchive(ctx, d, logger, w, contentMetadataOnly, nil, opt); err != nil {
			return fmt.Errorf("failed to add contents to archive: %w", err)
		}
	}

	if opt.WithSecrets || opt.WithSecretsMetadata {
		// If both full secrets and metadata-only are requested, prefer full secrets
		secretsMetadataOnly := opt.WithSecretsMetadata && !opt.WithSecrets
		if err := addSecretsToArchive(ctx, d, c, logger, w, secretsMetadataOnly, opt); err != nil {
			return fmt.Errorf("failed to add secrets to archive: %w", err)
		}
	}

	if err := addEventsToArchive(ctx, d, c, logger, w, opt); err != nil {
		return fmt.Errorf("failed to add events to archive: %w", err)
	}

	if err := addMetricsToArchive(ctx, c, logger, cfg, w, opt); err != nil {
		return fmt.Errorf("failed to add metrics to archive: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}

	if err := gz.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return nil
}

func addObjectsToArchive(
	ctx context.Context,
	dynamic dynamic.Interface,
	logger logr.Logger,
	g, v, r string,
	w *tar.Writer,
	opt Options,
) error {
	rID := schema.GroupVersionResource{
		Group:    g,
		Version:  v,
		Resource: r,
	}

	logger.V(1).Info("Fetching ...", "resource", rID.String())

	lo := metav1.ListOptions{Limit: opt.FetchLimit}
	for {
		var list *unstructured.UnstructuredList
		var err error

		// Apply namespace filtering when opt.Namespace is set and not in all-namespaces mode
		if opt.Namespace != "" && !opt.AllNamespaces {
			list, err = dynamic.Resource(rID).Namespace(opt.Namespace).List(ctx, lo)
		} else {
			list, err = dynamic.Resource(rID).List(ctx, lo)
		}

		if err != nil {
			return fmt.Errorf("failed to list %s: %w", r, err)
		}

		for _, i := range list.Items {
			g, err := yaml.Marshal(&i)
			if err != nil {
				return fmt.Errorf("failed to marshal %s: %w", r, err)
			}

			fileName := fmt.Sprintf("%s_%s_%s", r, i.GetNamespace(), i.GetName())
			if err := addFileToArchive(g, fileName, w); err != nil {
				return err
			}
		}

		c := list.GetContinue()
		if c == "" {
			break
		}
		lo.Continue = c
	}

	return nil
}

func addFileToArchive(data []byte, name string, w *tar.Writer) error {
	if err := w.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0644,
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0),
		Size:     int64(len(data)),
	}); err != nil {
		return err
	}
	_, err := w.Write(data)

	return err
}

func addContentsToArchive(
	ctx context.Context,
	dynamic dynamic.Interface,
	logger logr.Logger,
	w *tar.Writer,
	metadataOnly bool,
	contentIDs []string, // nil means fetch all
	opt Options,
) error {
	// Convert to map for faster lookups
	var contentIDMap map[string]bool
	if contentIDs != nil {
		contentIDMap = make(map[string]bool, len(contentIDs))
		for _, id := range contentIDs {
			contentIDMap[id] = true
		}
		logger.Info("Filtering content resources", "contentIDs", len(contentIDs))
	}

	rID := schema.GroupVersionResource{
		Group:    "fleet.cattle.io",
		Version:  "v1alpha1",
		Resource: "contents",
	}

	logger.V(1).Info("Fetching ...", "resource", rID.String())

	lo := metav1.ListOptions{Limit: opt.FetchLimit}
	for {
		list, err := dynamic.Resource(rID).List(ctx, lo)
		if err != nil {
			return fmt.Errorf("failed to list contents: %w", err)
		}

		for _, i := range list.Items {
			// Skip if filtering and this content ID is not in our filter set
			if contentIDMap != nil && !contentIDMap[i.GetName()] {
				continue
			}

			if metadataOnly {
				// Only strip the actual content (manifests), keep sha256sum and status as metadata
				i.Object["content"] = nil
			}

			g, err := yaml.Marshal(&i)
			if err != nil {
				return fmt.Errorf("failed to marshal content: %w", err)
			}

			fileName := fmt.Sprintf("contents_%s", i.GetName())
			if err := addFileToArchive(g, fileName, w); err != nil {
				return err
			}
		}

		c := list.GetContinue()
		if c == "" {
			break
		}
		lo.Continue = c
	}

	return nil
}

func addSecretsToArchive(
	ctx context.Context,
	dynamic dynamic.Interface,
	c client.Client,
	logger logr.Logger,
	w *tar.Writer,
	metadataOnly bool,
	opt Options,
) error {
	nss, err := getNamespaces(ctx, dynamic, logger, opt)
	if err != nil {
		return fmt.Errorf("failed to get relevant namespaces for secrets: %w", err)
	}

	var merr []error

nss:
	for _, ns := range nss {
		var secrets corev1.SecretList
		for {
			if err := c.List(ctx, &secrets, client.InNamespace(ns), client.Limit(opt.FetchLimit), client.Continue(secrets.Continue)); err != nil {
				merr = append(merr, fmt.Errorf("failed to list secrets for namespace %q: %w", ns, err))
				continue nss
			}

			for _, secret := range secrets.Items {
				if metadataOnly {
					secret.Data = nil
				}

				data, err := yaml.Marshal(&secret)
				if err != nil {
					merr = append(merr, fmt.Errorf("failed to marshal secret: %w", err))
					continue
				}

				fileName := fmt.Sprintf("secrets_%s_%s", secret.Namespace, secret.Name)
				if err := addFileToArchive(data, fileName, w); err != nil {
					merr = append(merr, fmt.Errorf("failed to add secret to archive: %w", err))
				}
			}

			if secrets.Continue == "" {
				break
			}
		}
	}

	return errutil.NewAggregate(merr)
}

// getNamespaces returns a list of namespaces relevant to the current context, containing:
// - kube-system
// - default
// - cattle-fleet-system
// - cattle-fleet-local-system
// - each cluster's namespace (only when not filtering by namespace)
// When namespace filtering is active (opt.Namespace is set and not AllNamespaces),
// returns only the filtered namespace plus system namespaces.
//
// TODO getNamespaces is called twice (for events and for secrets); consider caching the result.
func getNamespaces(ctx context.Context, dynamic dynamic.Interface, logger logr.Logger, opt Options) ([]string, error) {
	// Use a map to deduplicate namespaces
	nsMap := map[string]struct{}{
		"default":                   {},
		"kube-system":               {},
		config.DefaultNamespace:     {},
		"cattle-fleet-local-system": {},
	}

	// When filtering by namespace, just return the filtered namespace plus system namespaces
	if opt.Namespace != "" && !opt.AllNamespaces {
		nsMap[opt.Namespace] = struct{}{}

		// Convert map to slice and return early
		res := make([]string, 0, len(nsMap))
		for ns := range nsMap {
			res = append(res, ns)
		}
		return res, nil
	}

	// When not filtering, discover all cluster namespaces
	clusRscID := schema.GroupVersionResource{
		Group:    "fleet.cattle.io",
		Version:  "v1alpha1",
		Resource: "clusters",
	}

	lo := metav1.ListOptions{Limit: opt.FetchLimit}
	for {
		clusters, err := dynamic.Resource(clusRscID).List(ctx, lo)
		if err != nil {
			return nil, fmt.Errorf("failed to list clusters: %w", err)
		}

		for _, i := range clusters.Items {
			var c fleet.Cluster
			un, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&i)
			if err != nil {
				logger.Error(
					fmt.Errorf("resource %v", i),
					"Skipping resource listed as cluster but with incompatible format; this should not happen",
				)
				continue
			}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(un, &c); err != nil {
				logger.Error(
					fmt.Errorf("resource %v", i),
					"Skipping resource listed as cluster but with incompatible format; this should not happen",
				)
				continue
			}

			nsMap[c.Namespace] = struct{}{}
		}

		c := clusters.GetContinue()
		if c == "" {
			break
		}
		lo.Continue = c
	}

	// Convert map to slice
	res := make([]string, 0, len(nsMap))
	for ns := range nsMap {
		res = append(res, ns)
	}
	slices.Sort(res)

	return res, nil
}

// addEventsToArchive determines which namespaces to fetch events from, and for each of those namespaces where events
// are found, writes a file named `events_<namespace>` into w.
func addEventsToArchive(
	ctx context.Context,
	d dynamic.Interface,
	c client.Client,
	logger logr.Logger,
	w *tar.Writer,
	opt Options,
) error {
	nss, err := getNamespaces(ctx, d, logger, opt)
	if err != nil {
		return fmt.Errorf("failed to get relevant namespaces for events: %w", err)
	}

	var merr []error

	for _, ns := range nss {
		err := func() error {
			// Create a temporary file to accumulate events. We need to do this because we need to know
			// the total size of the events for the tar header, but we want to fetch and write events
			// page by page to avoid keeping them all in memory.
			tmpFile, err := os.CreateTemp("", fmt.Sprintf("events_%s_*.json", ns))
			if err != nil {
				return fmt.Errorf("failed to create temp file for events in namespace %q: %w", ns, err)
			}
			defer os.Remove(tmpFile.Name()) // Clean up temp file
			defer tmpFile.Close()           // Close file handle

			var NSevts corev1.EventList
			foundEvents := false

			// Write events page by page to temp file
			writeErr := false
			for {
				if err := c.List(ctx, &NSevts, client.InNamespace(ns), client.Limit(opt.FetchLimit), client.Continue(NSevts.Continue)); err != nil {
					merr = append(merr, fmt.Errorf("failed to list events for namespace %q: %w", ns, err))
					writeErr = true
					break
				}

				for _, e := range NSevts.Items {
					je, err := json.Marshal(e)
					if err != nil {
						merr = append(merr, fmt.Errorf("failed to encode event into JSON: %w", err))
						continue
					}

					if foundEvents {
						if _, err := tmpFile.Write([]byte("\n")); err != nil {
							merr = append(merr, fmt.Errorf("failed to write newline to temp file: %w", err))
							writeErr = true
							break
						}
					}

					if _, err := tmpFile.Write(je); err != nil {
						merr = append(merr, fmt.Errorf("failed to write event to temp file: %w", err))
						writeErr = true
						break
					}
					foundEvents = true
				}

				if writeErr || NSevts.Continue == "" {
					break
				}
			}

			if writeErr {
				return nil
			}

			if !foundEvents {
				return nil
			}

			// Get file size
			fileInfo, err := tmpFile.Stat()
			if err != nil {
				return fmt.Errorf("failed to stat temp file for namespace %q: %w", ns, err)
			}

			// Seek back to beginning to read
			if _, err := tmpFile.Seek(0, 0); err != nil {
				return fmt.Errorf("failed to seek temp file for namespace %q: %w", ns, err)
			}

			// Write tar header
			if err := w.WriteHeader(&tar.Header{
				Name:     fmt.Sprintf("events_%s", ns),
				Mode:     0644,
				Typeflag: tar.TypeReg,
				ModTime:  time.Unix(0, 0),
				Size:     fileInfo.Size(),
			}); err != nil {
				return fmt.Errorf("failed to write tar header for events in namespace %q: %w", ns, err)
			}

			// Copy temp file to tar
			if _, err := io.Copy(w, tmpFile); err != nil {
				return fmt.Errorf("failed to copy events to archive for namespace %q: %w", ns, err)
			}

			return nil
		}()

		if err != nil {
			merr = append(merr, err)
		}
	}

	return errutil.NewAggregate(merr)
}

func addMetricsToArchive(ctx context.Context, c client.Client, logger logr.Logger, cfg *rest.Config, w *tar.Writer, opt Options) error {
	ns := config.DefaultNamespace // XXX: support installation in non-default namespace, and check for services across all namespaces, by label?

	var monitoringSvcs []corev1.Service
	var svcs corev1.ServiceList
	for {
		opts := []client.ListOption{client.InNamespace(ns), client.Limit(opt.FetchLimit), client.Continue(svcs.Continue)}

		if err := c.List(ctx, &svcs, opts...); err != nil {
			return fmt.Errorf("failed to list services for extracting metrics: %w", err)
		}

		for _, svc := range svcs.Items {
			if !strings.HasPrefix(svc.Name, "monitoring-") {
				continue
			}

			monitoringSvcs = append(monitoringSvcs, svc)
		}

		if svcs.Continue == "" {
			break
		}
	}

	if len(monitoringSvcs) == 0 {
		logger.Info("No monitoring services found; Fleet has probably been installed with metrics disabled.", "namespace", ns)

		return nil
	}

	// XXX: how about HelmOps? report missing svc?
	for _, svc := range monitoringSvcs {
		closeFn, port, httpCli, err := forwardPorts(ctx, cfg, logger, c, &svc, opt)
		if err != nil {
			return fmt.Errorf("failed to forward ports: %w", err)
		}

		defer closeFn()

		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("http://localhost:%d/metrics", port),
			nil,
		)
		if err != nil {
			return fmt.Errorf("failed to create request to metrics service: %w", err)
		}

		resp, err := httpCli.Do(req)
		if err != nil {
			return fmt.Errorf("failed to get response from metrics service: %w", err)
		}

		defer func() {
			if resp.Body != nil {
				resp.Body.Close()
			}
		}()

		if resp.Body == nil {
			return fmt.Errorf("received empty response body from service %s/%s", svc.Namespace, svc.Name)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body from metrics service: %w", err)
		}

		logger.Info("Extracted metrics", "service", svc.Name)

		if err := addFileToArchive(body, fmt.Sprintf("metrics_%s", svc.Name), w); err != nil {
			return fmt.Errorf("failed to write metrics to archive from service %s: %w", svc.Name, err)
		}
	}

	return nil
}

func createClient(cfg *rest.Config) (client.Client, error) {
	c, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		return nil, err
	}

	return c, nil
}

func createDynamicClient(cfg *rest.Config) (dynamic.Interface, error) {
	di, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return di, nil
}

// createDialer creates a dialer needed to build a port forwarder from the service svc.
// It involves identifying the pod exposed by svc, since building a port forwarder using the service's K8s API URL
// directly does not work.
func createDialer(ctx context.Context, cfg *rest.Config, c client.Client, svc *corev1.Service, opt Options) (httpstream.Dialer, *http.Client, error) {
	var (
		appLabel   string
		shardKey   string
		shardValue string
	)
	for k, v := range svc.Spec.Selector {
		switch k {
		case "app":
			appLabel = v
			continue
		case sharding.ShardingIDLabel, sharding.ShardingDefaultLabel:
			shardKey = k
			shardValue = v
			continue
		}
	}

	if appLabel == "" {
		return nil, nil, fmt.Errorf("no app label found on service %s/%s", svc.Namespace, svc.Name)
	}

	var selectedPod *corev1.Pod

	matchingLabels := client.MatchingLabels{
		"app":    appLabel,
		shardKey: shardValue,
	}
	var pods corev1.PodList
	for {
		opts := []client.ListOption{
			client.InNamespace(svc.Namespace),
			matchingLabels,
			client.Limit(opt.FetchLimit),
			client.Continue(pods.Continue),
		}

		if err := c.List(ctx, &pods, opts...); err != nil {
			return nil, nil, fmt.Errorf("failed to get pod behind service %s/%s: %w", svc.Namespace, svc.Name, err)
		}

		for _, p := range pods.Items {
			if selectedPod == nil {
				podCopy := p
				selectedPod = &podCopy
			} else {
				return nil, nil, fmt.Errorf("found more than one pod behind service %s/%s", svc.Namespace, svc.Name)
			}
		}

		if pods.Continue == "" {
			break
		}
	}

	if selectedPod == nil {
		return nil, nil, fmt.Errorf("no pod found behind service %s/%s", svc.Namespace, svc.Name)
	}

	pod := *selectedPod

	rt, up, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create upgrader for fetching metrics: %w", err)
	}

	u, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create dialer for fetching metrics because the API server URL could not be parsed: %w", err)
	}

	u.Path = path.Join(u.Path, fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", pod.Namespace, pod.Name))

	u.Host = strings.TrimRight(u.Host, "/")

	httpCli := http.Client{Transport: rt}

	return spdy.NewDialer(up, &httpCli, http.MethodPost, u), &httpCli, nil
}

// forwardPorts creates a port forwarder for svc.
// In case of success, it returns a non-zero port number on which the service is available, an HTTP client which can
// later be used to query the service on the forwarded port, and a closing function.
// It is the caller's responsibility to call that closing function to close the port forwarder once it is no longer
// needed.
func forwardPorts(
	ctx context.Context,
	cfg *rest.Config,
	logger logr.Logger,
	c client.Client,
	svc *corev1.Service,
	opt Options,
) (func(), int, *http.Client, error) {
	fail := func(fmtStr string, args ...any) (func(), int, *http.Client, error) {
		return func() {}, 0, nil, fmt.Errorf(fmtStr, args...)
	}

	if len(svc.Spec.Ports) == 0 {
		return fail("service %s/%s does not have any exposed ports", svc.Namespace, svc.Name)
	}

	svcPort := svc.Spec.Ports[0].Port

	dl, httpCli, err := createDialer(ctx, cfg, c, svc, opt)
	if err != nil {
		return fail("failed to create dialer for port forwarding for service %s/%s: %w", svc.Namespace, svc.Name, err)
	}

	basePort := 8000
	var closeFn func()
	var port int

	// Keep trying to set up a port forwarder if failures happen, for instance because the chosen port is already in use
	maxAttempts := 5
	i := 0
	for i < maxAttempts {
		prefix := fmt.Sprintf("attempt %d: ", i+1)

		// Note on the `nolint: gosec` comment below: We are looking for an available port number; this can afford to be
		// fairly predictable.
		r := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint: gosec
		port = basePort + r.Intn(57535)                      // Highest possible port: 65534

		ports := []string{fmt.Sprintf("%d:%d", port, svcPort)}
		stopChan := make(chan struct{})
		readyChan := make(chan struct{})
		fwder, err := portforward.New(dl, ports, stopChan, readyChan, os.Stdout, os.Stderr)
		if err != nil {
			msg := "failed to create ports forwarder for fetching metrics"
			logger.Error(err, "%s%s", prefix, msg)

			if i < maxAttempts-1 {
				continue
			}

			return fail("%s%s: %w", prefix, msg, err)
		}

		errChan := make(chan error)

		go func() {
			if err := fwder.ForwardPorts(); err != nil {
				errChan <- err
			}
		}()

		closeFn = func() {
			fwder.Close()
			logger.Info("Closed port forwarding", "service", svc.Name, "port", port)
		}

		select {
		case <-readyChan:
			logger.Info("Port forwarding ready")
			i = maxAttempts // No need to keep trying
		case err = <-errChan:
			msg := "failed to forward ports for fetching metrics"
			logger.Error(err, "%s%s", prefix, msg)

			if i < maxAttempts-1 {
				i++
				continue
			}

			return fail("%s%s : %w", prefix, msg, err)
		}
	}

	return closeFn, port, httpCli, nil
}

// CreateWithClientsFiltered creates a dump with namespace filtering support.
// When opt.Namespace is set and opt.AllNamespaces is false, it filters resources
// intelligently based on their relationships:
// - GitRepos, Bundles, ClusterGroups, etc. are filtered by namespace
// - BundleDeployments are filtered by bundle-namespace label
// - Clusters may be in the bundle namespace or other namespaces
func CreateWithClientsFiltered(ctx context.Context, cfg *rest.Config, d dynamic.Interface, c client.Client, path string, opt Options) error {
	logger := log.FromContext(ctx).WithName("fleet-dump")

	tgz, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}

	gz := gzip.NewWriter(tgz)
	w := tar.NewWriter(gz)

	// Collect bundle metadata if filtering by namespace
	var bundleNames []string
	if !opt.AllNamespaces && opt.Namespace != "" {
		bundleNames, err = collectBundleNames(ctx, d, opt.Namespace, opt.FetchLimit)
		if err != nil {
			return fmt.Errorf("failed to collect bundle names: %w", err)
		}
		logger.Info("Filtering by namespace", "namespace", opt.Namespace, "bundles", len(bundleNames))
	}

	// Resources in the same namespace as GitRepos/Bundles
	sameNamespaceTypes := []string{
		"gitrepos",
		"bundles",
		"clusters",
		"clustergroups",
		"bundlenamespacemappings",
		"gitreporestrictions",
	}

	for _, t := range sameNamespaceTypes {
		if err := addObjectsToArchive(ctx, d, logger, "fleet.cattle.io", "v1alpha1", t, w, opt); err != nil {
			return fmt.Errorf("failed to add %s to archive: %w", t, err)
		}
	}

	// BundleDeployments: filter by bundle-namespace label when filtering
	if err := addBundleDeployments(ctx, d, logger, w, opt); err != nil {
		return fmt.Errorf("failed to add bundledeployments to archive: %w", err)
	}

	// HelmOps: filter by bundle-namespace label like BundleDeployments
	if err := addHelmOps(ctx, d, logger, w, opt); err != nil {
		return fmt.Errorf("failed to add helmops to archive: %w", err)
	}

	if opt.WithContent || opt.WithContentMetadata {
		contentMetadataOnly := opt.WithContentMetadata && !opt.WithContent

		var contentIDs []string
		if !opt.AllNamespaces && opt.Namespace != "" {
			var err error
			contentIDs, err = collectContentIDs(ctx, d, opt.Namespace, opt.FetchLimit)
			if err != nil {
				return fmt.Errorf("failed to collect content IDs from bundles: %w", err)
			}
			logger.Info("Collected content IDs from bundles", "count", len(contentIDs))
		}

		if err := addContentsToArchive(ctx, d, logger, w, contentMetadataOnly, contentIDs, opt); err != nil {
			return fmt.Errorf("failed to add contents to archive: %w", err)
		}
	}

	if opt.WithSecrets || opt.WithSecretsMetadata {
		secretsMetadataOnly := opt.WithSecretsMetadata && !opt.WithSecrets
		if err := addSecretsToArchive(ctx, d, c, logger, w, secretsMetadataOnly, opt); err != nil {
			return fmt.Errorf("failed to add secrets to archive: %w", err)
		}
	}

	if err := addEventsToArchive(ctx, d, c, logger, w, opt); err != nil {
		return fmt.Errorf("failed to add events to archive: %w", err)
	}

	if err := addMetricsToArchive(ctx, c, logger, cfg, w, opt); err != nil {
		return fmt.Errorf("failed to add metrics to archive: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}

	if err := gz.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return nil
}

// collectBundleNames fetches bundle names from the given namespace for filtering
func collectBundleNames(ctx context.Context, d dynamic.Interface, namespace string, fetchLimit int64) ([]string, error) {
	rID := schema.GroupVersionResource{
		Group:    "fleet.cattle.io",
		Version:  "v1alpha1",
		Resource: "bundles",
	}

	var names []string
	lo := metav1.ListOptions{Limit: fetchLimit}

	for {
		list, err := d.Resource(rID).Namespace(namespace).List(ctx, lo)
		if err != nil {
			return nil, fmt.Errorf("failed to list bundles: %w", err)
		}

		for _, item := range list.Items {
			names = append(names, item.GetName())
		}

		if list.GetContinue() == "" {
			break
		}
		lo.Continue = list.GetContinue()
	}

	return names, nil
}

// collectContentIDs fetches content IDs referenced by BundleDeployments associated with bundles in the given namespace.
// It queries BundleDeployments across all namespaces using the fleet.cattle.io/bundle-namespace label selector,
// then extracts content names from the fleet.cattle.io/content-name label.
func collectContentIDs(ctx context.Context, d dynamic.Interface, namespace string, fetchLimit int64) ([]string, error) {
	rID := schema.GroupVersionResource{
		Group:    "fleet.cattle.io",
		Version:  "v1alpha1",
		Resource: "bundledeployments",
	}

	contentIDMap := make(map[string]bool)
	lo := metav1.ListOptions{
		Limit:         fetchLimit,
		LabelSelector: fmt.Sprintf("fleet.cattle.io/bundle-namespace=%s", namespace),
	}

	for {
		// List BundleDeployments across all namespaces with the bundle-namespace label
		list, err := d.Resource(rID).List(ctx, lo)
		if err != nil {
			return nil, fmt.Errorf("failed to list bundledeployments: %w", err)
		}

		for _, item := range list.Items {
			// Extract the content-name label
			labels := item.GetLabels()
			if contentName, ok := labels["fleet.cattle.io/content-name"]; ok && contentName != "" {
				contentIDMap[contentName] = true
			}
		}

		if list.GetContinue() == "" {
			break
		}
		lo.Continue = list.GetContinue()
	}

	// Convert map to slice
	ids := make([]string, 0, len(contentIDMap))
	for id := range contentIDMap {
		ids = append(ids, id)
	}

	return ids, nil
}

// addBundleDeployments adds bundledeployment resources to the archive.
// When filtering by namespace, uses label selector for bundle-namespace.
func addBundleDeployments(ctx context.Context, d dynamic.Interface, logger logr.Logger, w *tar.Writer, opt Options) error {
	// When filtering by namespace, use label selector for bundle-namespace
	if !opt.AllNamespaces && opt.Namespace != "" {
		return addObjectsWithLabelSelector(ctx, d, logger, "fleet.cattle.io", "v1alpha1", "bundledeployments", w,
			fmt.Sprintf("fleet.cattle.io/bundle-namespace=%s", opt.Namespace), opt.FetchLimit)
	}
	return addObjectsToArchive(ctx, d, logger, "fleet.cattle.io", "v1alpha1", "bundledeployments", w, opt)
}

// addHelmOps adds helmop resources to the archive.
// When filtering by namespace, uses label selector for bundle-namespace like BundleDeployments.
func addHelmOps(ctx context.Context, d dynamic.Interface, logger logr.Logger, w *tar.Writer, opt Options) error {
	// Filter by bundle-namespace label like BundleDeployments
	if !opt.AllNamespaces && opt.Namespace != "" {
		return addObjectsWithLabelSelector(ctx, d, logger, "fleet.cattle.io", "v1alpha1", "helmops", w,
			fmt.Sprintf("fleet.cattle.io/bundle-namespace=%s", opt.Namespace), opt.FetchLimit)
	}
	return addObjectsToArchive(ctx, d, logger, "fleet.cattle.io", "v1alpha1", "helmops", w, opt)
}

// addObjectsWithLabelSelector fetches resources using a label selector (across all namespaces)
func addObjectsWithLabelSelector(ctx context.Context, d dynamic.Interface, logger logr.Logger, g, v, r string, w *tar.Writer, labelSelector string, fetchLimit int64) error {
	rID := schema.GroupVersionResource{
		Group:    g,
		Version:  v,
		Resource: r,
	}

	logger.V(1).Info("Fetching with label selector...", "resource", rID.String(), "labelSelector", labelSelector)

	lo := metav1.ListOptions{
		Limit:         fetchLimit,
		LabelSelector: labelSelector,
	}

	for {
		list, err := d.Resource(rID).List(ctx, lo)
		if err != nil {
			return fmt.Errorf("failed to list %s: %w", r, err)
		}

		for _, i := range list.Items {
			g, err := yaml.Marshal(&i)
			if err != nil {
				return fmt.Errorf("failed to marshal %s: %w", r, err)
			}

			fileName := fmt.Sprintf("%s_%s_%s", r, i.GetNamespace(), i.GetName())
			if err := addFileToArchive(g, fileName, w); err != nil {
				return err
			}
		}

		c := list.GetContinue()
		if c == "" {
			break
		}
		lo.Continue = c
	}

	return nil
}
