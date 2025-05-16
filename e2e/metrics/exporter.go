package metrics

import (
	"fmt"
	"net/http"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type ExporterTest struct {
	url string
}

func NewExporterTest(url string) ExporterTest {
	return ExporterTest{
		url: url,
	}
}

// Get fetches the metrics from the Prometheus endpoint and returns them
// as a map of metric families.
func (et *ExporterTest) Get() (map[string]*dto.MetricFamily, error) {
	resp, err := http.Get(et.url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var parser expfmt.TextParser
	metrics, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, err
	}
	return metrics, nil
}

// FindOneMetric expects to find exactly one metric with the given name, resource name,
// resource namespace, and labels. If no such metric is found, or if more than
// one is found, an error is returned.
//
// `resourceName` and `resourceNamespace` are the values of the `name` and
// `namespace` labels, respectively.
//
// If labels is nil, only the name and namespace labels are checked.
func (m *ExporterTest) FindOneMetric(
	allMetrics map[string]*dto.MetricFamily,
	name string,
	labels map[string]string,
) (*Metric, error) {
	// check if name exists.
	mf, ok := allMetrics[name]
	if !ok {
		return nil, fmt.Errorf("metric %q with labels %v not found for URL %v", name, labels, m.url)
	}

	var metrics []*dto.Metric
	for _, metric := range mf.Metric {
		m := Metric{Metric: metric}

		// Check that all labels match, if present.
		match := true
		for k, v := range labels {
			if m.LabelValue(k) != v {
				match = false
				break
			}
		}
		if match {
			metrics = append(metrics, metric)
		}
	}

	if len(metrics) != 1 {
		return nil, fmt.Errorf(
			"expected to find 1 metric for %s{%s}, got %d",
			name,
			promLabels(labels),
			len(metrics),
		)
	}

	return &Metric{Metric: metrics[0]}, nil
}

type promLabels map[string]string

func (l promLabels) String() string {
	labels := make([]string, 0, len(l))
	for k, v := range l {
		labels = append(labels, fmt.Sprintf("%s=%q", k, v))
	}
	return strings.Join(labels, ",")
}

type Metric struct {
	*dto.Metric
}

// LabelValue returns the value of the label with the given name. If no such
// label is found, an empty string is returned.
func (m *Metric) LabelValue(name string) string {
	for _, label := range m.Label {
		if *label.Name == name {
			return *label.Value
		}
	}
	return ""
}
