package doctor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v2"
	"sigs.k8s.io/controller-runtime/pkg/log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// func CreateReport(ctx context.Context, cfg *rest.Config, path string) error {
// func CreateReport(ctx context.Context, ri rest.Interface, path string) error {
func CreateReport(ctx context.Context, d dynamic.Interface, path string) error {
	logger := log.FromContext(ctx).WithName("fleet-doctor-report")
	// XXX: be smart about memory management, progressively write data to disk instead of keeping everything in memory?

	// XXX: order by namespace or by kind?
	//d := dynamic.New(ri)

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
	// TODO add k8s events

	// TODO add log messages (e.g. for each resource, with count to give an idea) w/ verbosity level to enable users to know what the command is
	// doing, for instance at scale where it is likely to take longer

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

		if err := w.WriteHeader(&tar.Header{
			Name:     fmt.Sprintf("%s_%s", r, i.GetName()),
			Mode:     0644,
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
			Size:     int64(len(g)),
		}); err != nil {
			return err
		}
		_, err = w.Write(g)
		if err != nil {
			return err
		}
	}

	return nil
}
