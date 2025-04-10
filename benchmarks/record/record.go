package record

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"maps"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gm "github.com/onsi/gomega/gmeasure"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	k         kubectl.Command
	k8sClient client.Client
	workspace string
)

func Setup(w string, k8s client.Client, kcmd kubectl.Command) {
	workspace = w
	k8sClient = k8s
	k = kcmd
}

func MemoryUsage(experiment *gm.Experiment, name string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	experiment.RecordValue(name, float64(m.Alloc/1024/1024), gm.Precision(0), gm.Units("MB"))
}

func ResourceCount(ctx context.Context, experiment *gm.Experiment, name string) {
	n := 0

	clusters := &v1alpha1.ClusterList{}
	Expect(k8sClient.List(ctx, clusters, client.InNamespace(workspace))).To(Succeed())
	n += len(clusters.Items)

	clusterGroups := &v1alpha1.ClusterGroupList{}
	Expect(k8sClient.List(ctx, clusterGroups, client.InNamespace(workspace))).To(Succeed())
	n += len(clusterGroups.Items)

	gitRepos := &v1alpha1.GitRepoList{}
	Expect(k8sClient.List(ctx, gitRepos, client.InNamespace(workspace))).To(Succeed())
	n += len(gitRepos.Items)

	contents := &v1alpha1.ContentList{}
	Expect(k8sClient.List(ctx, contents, client.InNamespace(workspace))).To(Succeed())
	n += len(contents.Items)

	bundles := &v1alpha1.BundleList{}
	Expect(k8sClient.List(ctx, bundles, client.InNamespace(workspace))).To(Succeed())
	n += len(bundles.Items)

	deployments := &v1alpha1.BundleDeploymentList{}
	Expect(k8sClient.List(ctx, deployments, client.InNamespace(workspace))).To(Succeed())
	n += len(deployments.Items)

	serviceAccounts := &corev1.ServiceAccountList{}
	Expect(k8sClient.List(ctx, serviceAccounts, client.InNamespace(workspace))).To(Succeed())
	n += len(serviceAccounts.Items)

	roles := &rbacv1.RoleList{}
	Expect(k8sClient.List(ctx, roles, client.InNamespace(workspace))).To(Succeed())
	n += len(roles.Items)

	roleBindings := &rbacv1.RoleBindingList{}
	Expect(k8sClient.List(ctx, roleBindings, client.InNamespace(workspace))).To(Succeed())
	n += len(roleBindings.Items)

	clusterRoles := &rbacv1.ClusterRoleList{}
	Expect(k8sClient.List(ctx, clusterRoles)).To(Succeed())
	n += len(clusterRoles.Items)

	clusterRoleBindings := &rbacv1.ClusterRoleBindingList{}
	Expect(k8sClient.List(ctx, clusterRoleBindings)).To(Succeed())
	n += len(clusterRoleBindings.Items)

	deps := &appsv1.DeploymentList{}
	Expect(k8sClient.List(ctx, deps, client.InNamespace(workspace))).To(Succeed())
	n += len(deps.Items)

	statefulSets := &appsv1.StatefulSetList{}
	Expect(k8sClient.List(ctx, statefulSets, client.InNamespace(workspace))).To(Succeed())
	n += len(statefulSets.Items)

	pods := &corev1.PodList{}
	Expect(k8sClient.List(ctx, pods, client.InNamespace(workspace))).To(Succeed())
	n += len(pods.Items)

	jobs := &batchv1.JobList{}
	Expect(k8sClient.List(ctx, jobs, client.InNamespace(workspace))).To(Succeed())
	n += len(jobs.Items)

	services := &corev1.ServiceList{}
	Expect(k8sClient.List(ctx, services, client.InNamespace(workspace))).To(Succeed())
	n += len(services.Items)

	configMaps := &corev1.ConfigMapList{}
	Expect(k8sClient.List(ctx, configMaps, client.InNamespace(workspace))).To(Succeed())
	n += len(configMaps.Items)

	secrets := &corev1.SecretList{}
	Expect(k8sClient.List(ctx, secrets, client.InNamespace(workspace))).To(Succeed())
	n += len(secrets.Items)

	volumes := &corev1.PersistentVolumeList{}
	Expect(k8sClient.List(ctx, volumes)).To(Succeed())
	n += len(volumes.Items)

	ingresses := &networkingv1.IngressList{}
	Expect(k8sClient.List(ctx, ingresses, client.InNamespace(workspace))).To(Succeed())
	n += len(ingresses.Items)

	namespaces := &corev1.NamespaceList{}
	Expect(k8sClient.List(ctx, namespaces)).To(Succeed())
	n += len(namespaces.Items)

	experiment.RecordValue(name, float64(n), gm.Precision(0), gm.Units("resources"))
}

func CRDCount(ctx context.Context, setup *gm.Experiment, name string) {
	crds := &apiextv1.CustomResourceDefinitionList{}
	Expect(k8sClient.List(ctx, crds)).To(Succeed())
	setup.RecordValue(name, float64(len(crds.Items)), gm.Precision(0), gm.Units("CRDs"))
}

func Clusters(ctx context.Context, setup *gm.Experiment) {
	clusters := &v1alpha1.ClusterList{}
	Expect(k8sClient.List(ctx, clusters, client.InNamespace(workspace))).To(Succeed())
	setup.RecordValue("ClusterCount", float64(len(clusters.Items)), gm.Precision(0), gm.Units("clusters"))
}

func Nodes(ctx context.Context, experiment *gm.Experiment) {
	nodes := &corev1.NodeList{}
	Expect(k8sClient.List(ctx, nodes)).To(Succeed())
	experiment.RecordValue("NodeCount", float64(len(nodes.Items)), gm.Precision(0), gm.Units("nodes"))

	var sb strings.Builder
	sb.WriteString("CPU, Memory, Pods\n")
	cpu := resource.NewQuantity(0, resource.DecimalSI)
	mem := resource.NewQuantity(0, resource.DecimalSI)
	pods := resource.NewQuantity(0, resource.DecimalSI)
	images := make(map[string]struct{})
	for _, node := range nodes.Items {
		cpu.Add(*node.Status.Capacity.Cpu())
		mem.Add(*node.Status.Capacity.Memory())
		pods.Add(*node.Status.Capacity.Pods())
		// A single image has multiple names. Pick the second name as
		// it has a human readable tag.
		for _, image := range node.Status.Images {
			name := ""
			// in k3d, the first image name contains the hash, not the tag
			if len(image.Names) == 0 {
				continue
			} else if len(image.Names) > 1 {
				name = image.Names[1]
			} else {
				name = image.Names[0]
			}
			images[name] = struct{}{}
		}
		sb.WriteString(fmt.Sprintf("%s, %s, %s\n",
			node.Status.Capacity.Cpu().String(),
			node.Status.Capacity.Memory().String(),
			node.Status.Capacity.Pods().String()))
	}
	experiment.RecordNote(Header("Node Resources")+sb.String(), gm.Style("{{green}}"))
	img := strings.Join(slices.Sorted(maps.Keys(images)), ", ")
	experiment.RecordNote(Header("Images")+img, gm.Style("{{green}}"))
	sumCPU, _ := cpu.AsInt64()
	experiment.RecordValue("SumCPU", float64(sumCPU), gm.Precision(0), gm.Units("cores"))
	sumMem, _ := mem.AsInt64()
	experiment.RecordValue("SumMem", float64(sumMem/1024/1024), gm.Precision(0), gm.Units("MB"))
	sumPods, _ := pods.AsInt64()
	experiment.RecordValue("SumPods", float64(sumPods), gm.Precision(0), gm.Units("pods"))
}

func Header(s string) string {
	h := fmt.Sprintf("%s\n", s)
	h += strings.Repeat("=", len(s)) + "\n"
	return h
}

func Metrics(experiment *gm.Experiment, suffix string) {
	res := map[string]float64{}

	getMetrics(res, "monitoring-fleet-controller.cattle-fleet-system.svc.cluster.local:8080/metrics", "bundle", "bundledeployment", "cluster", "clustergroup", "imagescan")

	getMetrics(res, "monitoring-gitjob.cattle-fleet-system.svc.cluster.local:8081/metrics", "GitRepoStatus", "gitrepo")

	for k, v := range res {
		n := k + suffix
		switch k {
		case "ReconcileErrors", "ReconcileRequeue", "ReconcileRequeueAfter", "ReconcileSuccess":
			experiment.RecordValue(n, v, gm.Precision(0), gm.Units("reconciles"))
		case "GCDuration", "CPU", "ReconcileTime", "WorkqueueQueueDuration", "WorkqueueWorkDuration":
			experiment.RecordValue(n, v, gm.Precision(1), gm.Units("seconds"))
		case "WorkqueueAdds", "WorkqueueRetries":
			experiment.RecordValue(n, v, gm.Precision(0), gm.Units("items"))
		case "NetworkRX", "NetworkTX":
			experiment.RecordValue(n, v, gm.Precision(0), gm.Units("bytes"))
		default:
			if strings.HasPrefix(k, "RESTClient") {
				experiment.RecordValue(n, v, gm.Precision(0), gm.Units("requests"))
			} else {
				experiment.RecordValue(n, v, gm.Precision(0))
			}
		}
	}
}

func getMetrics(res map[string]float64, url string, controllers ...string) {
	pod := addRandomSuffix("curl")
	var (
		mfs    map[string]*dto.MetricFamily
		parser expfmt.TextParser
	)
	Eventually(func() error {
		GinkgoWriter.Print("Fetching metrics from " + url + "\n")
		out, err := k.Run("run", "--rm", "--attach", "--quiet", "--restart=Never", pod, "--image=curlimages/curl", "--namespace", "cattle-fleet-system", "--command", "--", "curl", "-s", url)
		if err != nil {
			return err
		}

		mfs, err = parser.TextToMetricFamilies(bytes.NewBufferString(out))
		if err != nil {
			return err
		}

		if _, ok := mfs["controller_runtime_reconcile_total"]; !ok {
			return fmt.Errorf("controller_runtime_reconcile_total not found")
		}

		return nil
	}).Should(Succeed())

	extractFromMetricFamilies(res, controllers, mfs)
}

// addRandomSuffix adds a random suffix to a given name.
func addRandomSuffix(name string) string {
	p := make([]byte, 4)
	binary.LittleEndian.PutUint32(p, rand.Uint32())

	return fmt.Sprintf("%s-%s", name, hex.EncodeToString(p))
}

func extractFromMetricFamilies(res map[string]float64, controllers []string, mfs map[string]*dto.MetricFamily) {
	// Example input:
	// controller_runtime_reconcile_total{controller="gitrepo",result="error"} 0
	// controller_runtime_reconcile_total{controller="gitrepo",result="requeue"} 71
	// controller_runtime_reconcile_total{controller="gitrepo",result="requeue_after"} 155
	// controller_runtime_reconcile_total{controller="gitrepo",result="success"} 267
	mf := mfs["controller_runtime_reconcile_total"]
	for _, m := range mf.Metric {
		l := m.GetLabel()
		for _, c := range controllers {
			if l[0].GetValue() == c {
				v := m.Counter.GetValue()
				switch l[1].GetValue() {
				case "error":
					res["ReconcileErrors"] += v
				case "requeue":
					res["ReconcileRequeue"] += v
				case "requeue_after":
					res["ReconcileRequeueAfter"] += v
				case "success":
					res["ReconcileSuccess"] += v

				}

				break
			}
		}
	}
	// rest_client_requests_total{code="200",host="10.43.0.1:443",method="DELETE"} 791
	// rest_client_requests_total{code="200",host="10.43.0.1:443",method="GET"} 1432
	// rest_client_requests_total{code="200",host="10.43.0.1:443",method="PATCH"} 213
	// rest_client_requests_total{code="200",host="10.43.0.1:443",method="PUT"} 15986
	// rest_client_requests_total{code="201",host="10.43.0.1:443",method="POST"} 2088
	// rest_client_requests_total{code="404",host="10.43.0.1:443",method="GET"} 1
	// rest_client_requests_total{code="409",host="10.43.0.1:443",method="POST"} 6
	// rest_client_requests_total{code="409",host="10.43.0.1:443",method="PUT"} 1392
	mf = mfs["rest_client_requests_total"]
	for _, m := range mf.Metric {
		l := m.GetLabel()
		v := m.Counter.GetValue()
		code := ""
		method := ""
		for _, tmp := range l {
			switch tmp.GetName() {
			case "code":
				code = tmp.GetValue()
			case "method":
				method = tmp.GetValue()
			}
		}
		if code != "" && method != "" {
			res["RESTClient"+method+code] = v
		}
	}

	// Example input:
	// controller_runtime_reconcile_time_seconds_sum{controller="gitrepo"} 185.52245399500018
	mf = mfs["controller_runtime_reconcile_time_seconds"]
	incMetric(res, "ReconcileTime", controllers, *mf.Type, mf.Metric)

	mf = mfs["workqueue_adds_total"]
	incMetric(res, "WorkqueueAdds", controllers, *mf.Type, mf.Metric)

	mf = mfs["workqueue_queue_duration_seconds"]
	incMetric(res, "WorkqueueQueueDuration", controllers, *mf.Type, mf.Metric)

	mf = mfs["workqueue_retries_total"]
	incMetric(res, "WorkqueueRetries", controllers, *mf.Type, mf.Metric)

	mf = mfs["workqueue_work_duration_seconds"]
	incMetric(res, "WorkqueueWorkDuration", controllers, *mf.Type, mf.Metric)

	for _, m := range mfs["go_gc_duration_seconds"].Metric {
		res["GCDuration"] += m.Summary.GetSampleSum()
	}

	for _, m := range mfs["process_cpu_seconds_total"].Metric {
		res["CPU"] += m.Counter.GetValue()
	}

	for _, m := range mfs["process_network_receive_bytes_total"].Metric {
		res["NetworkRX"] += m.Counter.GetValue()
	}

	for _, m := range mfs["process_network_transmit_bytes_total"].Metric {
		res["NetworkTX"] += m.Counter.GetValue()
	}
}

func incMetric(res map[string]float64, name string, controllers []string, t dto.MetricType, metrics []*dto.Metric) {
	for _, m := range metrics {
		l := m.GetLabel()
		for _, c := range controllers {
			if l[0].GetValue() == c {
				switch t {
				case dto.MetricType_COUNTER:
					res[name] += m.Counter.GetValue()
				case dto.MetricType_GAUGE:
					res[name] += m.Gauge.GetValue()
				case dto.MetricType_SUMMARY:
					res[name] += m.Summary.GetSampleSum()
				case dto.MetricType_HISTOGRAM:
					res[name] += m.Histogram.GetSampleSum()
				}
			}
		}
	}
}
