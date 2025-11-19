package metrics

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
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

	objMetrics = []prometheus.Collector{}
)

func registerObjMetrics() {
	for _, metric := range objMetrics {
		metrics.Registry.MustRegister(metric)
	}
}

func RegisterMetrics() {
	GitRepoCollector.Register()
	ClusterCollector.Register()
	ClusterGroupCollector.Register()
	BundleCollector.Register()
	BundleDeploymentCollector.Register()

	registerObjMetrics()
}

func RegisterGitOptsMetrics() {
	GitRepoCollector.Register()

	registerObjMetrics()
}

func RegisterHelmOpsMetrics() {
	HelmCollector.Register()

	registerObjMetrics()
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
	defer func() {
		if r := recover(); r != nil {
			logger.Error(errors.New("error collecting metrics"), "observed panic", "panic", r)
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

func getStatusMetrics(subsystem string, labels []string) map[string]prometheus.Collector {
	return map[string]prometheus.Collector{
		"resources_desired_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_desired_ready",
				Help:      "The count of resources that are desired to be in a Ready state.",
			},
			labels,
		),
		"resources_missing": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_missing",
				Help:      "The count of resources that are in a Missing state.",
			},
			labels,
		),
		"resources_modified": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_modified",
				Help:      "The count of resources that are in a Modified state.",
			},
			labels,
		),
		"resources_not_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_not_ready",
				Help:      "The count of resources that are in a NotReady state.",
			},
			labels,
		),
		"resources_orphaned": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_orphaned",
				Help:      "The count of resources that are in an Orphaned state.",
			},
			labels,
		),
		"resources_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_ready",
				Help:      "The count of resources that are in a Ready state.",
			},
			labels,
		),
		"resources_unknown": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_unknown",
				Help:      "The count of resources that are in an Unknown state.",
			},
			labels,
		),
		"resources_wait_applied": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "resources_wait_applied",
				Help:      "The count of resources that are in a WaitApplied state.",
			},
			labels,
		),
		"desired_ready_clusters": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "desired_ready_clusters",
				Help:      "The amount of clusters desired to be in a ready state.",
			},
			labels,
		),
		"ready_clusters": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: subsystem,
				Name:      "ready_clusters",
				Help:      "The count of clusters in a Ready state.",
			},
			labels,
		),
	}
}

// ObjCounter creates and registers a new CounterVec metric with the given name and help
// text. The returned CounterVec embeds the CounterVec from the prometheus package and adds a method
// to increment the counter for a given object. The labels of the metric are determined from the
// name and the namespace of the given object.
func ObjCounter(name, help string) (c ObjCounterVec) {
	labels := []string{"name", "namespace"}

	counterVec := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricPrefix,
			Name:      name,
			Help:      help,
		},
		labels,
	)

	objMetrics = append(objMetrics, counterVec)

	return ObjCounterVec{
		counterVec: counterVec,
		labels:     labels,
	}
}

// ObjCounterVec is a wrapper around prometheus.CounterVec that adds a method to increment the
// counter for a given metav1 object. The labels of the metric are determined from the name and the
type ObjCounterVec struct {
	counterVec *prometheus.CounterVec
	labels     []string
}

func (m *ObjCounterVec) Inc(obj metav1.Object) {
	m.counterVec.WithLabelValues(obj.GetName(), obj.GetNamespace()).Inc()
}

func (m *ObjCounterVec) DeleteByReq(req ctrl.Request) bool {
	return m.counterVec.DeleteLabelValues(req.Name, req.Namespace)
}

var BucketsLatency = []float64{.1, .2, .5, 1, 2, 5, 10, 30}

func ObjHistogram(name, help string, buckets []float64) (h ObjHistogramVec) {
	histogram := promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricPrefix,
			Name:      name,
			Help:      help,
			Buckets:   buckets,
		},
		[]string{"name", "namespace"},
	)

	objMetrics = append(objMetrics, histogram)

	return ObjHistogramVec{
		histogram: histogram,
		labels:    []string{"name", "namespace"},
	}
}

type ObjHistogramVec struct {
	histogram *prometheus.HistogramVec
	labels    []string
}

func (m *ObjHistogramVec) Observe(obj metav1.Object, value float64) {
	m.histogram.WithLabelValues(obj.GetName(), obj.GetNamespace()).Observe(value)
}

func (m *ObjHistogramVec) DeleteByReq(req ctrl.Request) bool {
	return m.histogram.DeleteLabelValues(req.Name, req.Namespace)
}

func ObjGauge(name, help string) (g ObjGaugeVec) {
	gauge := promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricPrefix,
			Name:      name,
			Help:      help,
		},
		[]string{"name", "namespace"},
	)

	objMetrics = append(objMetrics, gauge)

	return ObjGaugeVec{
		gauge:  gauge,
		labels: []string{"name", "namespace"},
	}
}

type ObjGaugeVec struct {
	gauge  *prometheus.GaugeVec
	labels []string
}

func (m *ObjGaugeVec) Set(obj metav1.Object, value float64) {
	m.gauge.WithLabelValues(obj.GetName(), obj.GetNamespace()).Set(value)
}

func (m *ObjGaugeVec) Delete(obj metav1.Object) bool {
	return m.gauge.DeleteLabelValues(obj.GetName(), obj.GetNamespace())
}
