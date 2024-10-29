package metrics

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricPrefix = "fleet"
)

var (
	bundleStates = []fleet.BundleState{
		fleet.Ready,
		fleet.NotReady,
		fleet.Pending,
		fleet.OutOfSync,
		fleet.Modified,
		fleet.WaitApplied,
		fleet.ErrApplied,
	}
	enabled = false
)

func RegisterMetrics() {
	enabled = true

	GitRepoCollector.Register()
	ClusterCollector.Register()
	ClusterGroupCollector.Register()
	BundleCollector.Register()
	BundleDeploymentCollector.Register()
}

func RegisterGitOptsMetrics() {
	enabled = true

	GitRepoCollector.Register()
}

// CollectorCollection implements the generic methods `Delete` and `Register`
// for a collection of Prometheus collectors. It is used to manage the lifecycle
// of a collection of Prometheus collectors.
type CollectorCollection struct {
	subsystem string
	metrics   map[string]prometheus.Collector
	collector func(obj any, metrics map[string]prometheus.Collector)
}

// Collect collects the metrics for the given object. It deletes the metrics for
// the object if they already exist and then collects the metrics for the
// object.
//
// The metrics need to be deleted because the values of the metrics may have
// changed and this would create a new instance of those metrics, keeping the
// old one around. Metrics are deleted by their name and namespace label values.
func (c *CollectorCollection) Collect(ctx context.Context, obj metav1.ObjectMetaAccessor) {
	logger := log.FromContext(ctx).WithName("metrics")
	if !enabled {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			msg, ok := r.(string)
			if !ok {
				msg = "unexpected error"
			}
			logger.Error(errors.New("error collecting metrics"), msg, r)
		}
	}()
	c.Delete(obj.GetObjectMeta().GetName(), obj.GetObjectMeta().GetNamespace())
	c.collector(obj, c.metrics)
}

// Delete deletes the metric with the given name and namespace labels. It
// returns the number of metrics deleted. It does a DeletePartialMatch on the
// metric with the given name and namespace labels.
func (c *CollectorCollection) Delete(name, namespace string) (deleted int) {
	identityLabels := prometheus.Labels{
		"name":      name,
		"namespace": namespace,
	}
	for _, collector := range c.metrics {
		switch metric := collector.(type) {
		case *prometheus.MetricVec:
			deleted += metric.DeletePartialMatch(identityLabels)
		case *prometheus.CounterVec:
			deleted += metric.DeletePartialMatch(identityLabels)
		case *prometheus.GaugeVec:
			deleted += metric.DeletePartialMatch(identityLabels)
		default:
			panic("unexpected metric type")
		}
	}

	return deleted
}

func (c *CollectorCollection) Register() {
	for _, metric := range c.metrics {
		metrics.Registry.MustRegister(metric)
	}
}
