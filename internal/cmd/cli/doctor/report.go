package doctor

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
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/rancher/fleet/internal/config"
	"gopkg.in/yaml.v2"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func CreateReport(ctx context.Context, cfg *rest.Config, path string) error {
	c, err := createClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	d, err := createDynamicClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create dynamic Kubernetes client: %w", err)
	}

	return CreateReportWithClients(ctx, cfg, d, c, path)
}

func CreateReportWithClients(ctx context.Context, cfg *rest.Config, d dynamic.Interface, c client.Client, path string) error {
	logger := log.FromContext(ctx).WithName("fleet-doctor-report")
	// XXX: be smart about memory management, progressively write data to disk instead of keeping everything in memory?

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
		//"contents",
	}

	for _, t := range types {
		if err := addObjectsToArchive(ctx, d, logger, "fleet.cattle.io", "v1alpha1", t, w); err != nil {
			return fmt.Errorf("failed to add %s to archive: %w", t, err)
		}
	}

	if err := addEventsToArchive(ctx, d, c, logger, w); err != nil {
		return fmt.Errorf("failed to add events to archive: %w", err)
	}

	if err := addMetricsToArchive(ctx, c, logger, cfg, w); err != nil {
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
) error {
	rID := schema.GroupVersionResource{
		Group:    g,
		Version:  v,
		Resource: r,
	}

	logger.V(1).Info("Fetching ...", "resource", rID.String())

	list, err := dynamic.Resource(rID).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list %s: %w", r, err)
	}

	for _, i := range list.Items {
		g, err := yaml.Marshal(&i)
		if err != nil {
			return fmt.Errorf("failed to marshal %s: %w", r, err)
		}

		fileName := fmt.Sprintf("%s_%s", r, i.GetName())
		if err := addFileToArchive(g, fileName, w); err != nil {
			return err
		}
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

// getNamespaces returns a list of namespaces relevant to the current context, containing:
// - kube-system
// - default
// - cattle-fleet-system
// - cattle-fleet-local-system
// - each cluster's namespace
func getNamespaces(ctx context.Context, dynamic dynamic.Interface, logger logr.Logger) ([]string, error) {
	res := []string{
		"default",
		"kube-system",
		config.DefaultNamespace,
		"cattle-fleet-local-system",
	}

	clusRscID := schema.GroupVersionResource{
		Group:    "fleet.cattle.io",
		Version:  "v1alpha1",
		Resource: "clusters",
	}

	clusters, err := dynamic.Resource(clusRscID).List(ctx, metav1.ListOptions{})
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

		res = append(res, c.Namespace)
	}

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
) error {
	nss, err := getNamespaces(ctx, d, logger)
	if err != nil {
		return fmt.Errorf("failed to get relevant namespaces for events: %w", err)
	}

	var merr []error

	for _, ns := range nss {
		var NSevts corev1.EventList
		if err := c.List(ctx, &NSevts, client.InNamespace(ns)); err != nil {
			return fmt.Errorf("failed to list events for namespace %q: %w", ns, err)
		}

		var data []byte

		for i, e := range NSevts.Items {
			je, err := json.Marshal(e)
			if err != nil {
				merr = append(merr, fmt.Errorf("failed to encode event into JSON: %w", err))
			}

			data = append(data, je...)

			if i < len(NSevts.Items)-1 {
				data = append(data, []byte("\n")...)
			}
		}

		if len(NSevts.Items) > 0 {
			if err := addFileToArchive(data, fmt.Sprintf("events_%s", ns), w); err != nil {
				merr = append(merr, fmt.Errorf("failed to add events to archive for namespace %q: %w", ns, err))
			}
		}
	}

	return errutil.NewAggregate(merr)
}

func addMetricsToArchive(ctx context.Context, c client.Client, logger logr.Logger, cfg *rest.Config, w *tar.Writer) error {
	ns := config.DefaultNamespace // XXX: support installation in non-default namespace, and check for services across all namespaces, by label?

	var svcs corev1.ServiceList
	if err := c.List(ctx, &svcs, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("failed to list services for extracting metrics: %w", err)
	}

	var monitoringSvcs []corev1.Service
	for _, svc := range svcs.Items {
		if !strings.HasPrefix(svc.Name, "monitoring-") {
			continue
		}

		monitoringSvcs = append(monitoringSvcs, svc)
	}

	if len(monitoringSvcs) == 0 {
		logger.Info("No monitoring services found; Fleet has probably been installed with metrics disabled.", "namespace", ns)

		return nil
	}

	// XXX: how about HelmOps? report missing svc?
	for _, svc := range monitoringSvcs {
		closeFn, port, httpCli, err := forwardPorts(ctx, cfg, logger, c, &svc)
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
func createDialer(ctx context.Context, cfg *rest.Config, c client.Client, svc *corev1.Service) (httpstream.Dialer, *http.Client, error) {
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

	var pods corev1.PodList

	matchingLabels := client.MatchingLabels{
		"app":    appLabel,
		shardKey: shardValue,
	}

	if err := c.List(ctx, &pods, client.InNamespace(svc.Namespace), matchingLabels); err != nil {
		return nil, nil, fmt.Errorf("failed to get pod behind service %s/%s: %w", svc.Namespace, svc.Name, err)
	}

	if len(pods.Items) == 0 {
		return nil, nil, fmt.Errorf("no pod found behind service %s/%s", svc.Namespace, svc.Name)
	}

	if len(pods.Items) > 1 {
		return nil, nil, fmt.Errorf("found more than one pod behind service %s/%s", svc.Namespace, svc.Name)
	}

	pod := pods.Items[0]

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
) (func(), int, *http.Client, error) {
	fail := func(fmtStr string, args ...any) (func(), int, *http.Client, error) {
		return func() {}, 0, nil, fmt.Errorf(fmtStr, args...)
	}

	if len(svc.Spec.Ports) == 0 {
		return fail("service %s/%s does not have any exposed ports", svc.Namespace, svc.Name)
	}

	svcPort := svc.Spec.Ports[0].Port

	dl, httpCli, err := createDialer(ctx, cfg, c, svc)
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

		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		port = basePort + r.Intn(57535) // Highest possible port: 65534

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
