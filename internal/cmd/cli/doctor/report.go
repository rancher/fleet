package doctor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/rancher/fleet/internal/config"
	"gopkg.in/yaml.v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/dynamic"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func CreateReport(ctx context.Context, d dynamic.Interface, c client.Client, path string) error {
	logger := log.FromContext(ctx).WithName("fleet-doctor-report")
	// XXX: be smart about memory management, progressively write data to disk instead of keeping everything in memory?

	// XXX: order by namespace or by kind?

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

	// TODO add metrics
	if err := addEventsToArchive(ctx, d, c, logger, w); err != nil {
		return fmt.Errorf("failed to add events to archive: %w", err)
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
