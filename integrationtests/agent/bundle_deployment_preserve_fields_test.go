package agent_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	boundPVCName         = "data"
	boundPVCStorageClass = "standard"
	// immutableSpecError is the API server's error for a PUT which changes the
	// spec of a bound claim.
	immutableSpecError = "spec is immutable after creation"
)

const pvcTemplate = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
  labels:
    rev: %q
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 1Gi
`

// neverReadyDeployment never becomes ready in envtest, which runs no
// controllers, so a Helm upgrade waiting for it times out.
const neverReadyDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: never-ready
spec:
  selector:
    matchLabels:
      app: never-ready
  template:
    metadata:
      labels:
        app: never-ready
    spec:
      containers:
      - name: c
        image: busybox
`

const preservedConfigMapTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: preserved
data:
  rev: %q
`

// hookJobTemplate is a Job which Helm runs as a hook after each install and
// upgrade.
const hookJobTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: job
  labels:
    rev: %q
  annotations:
    helm.sh/hook: post-install,post-upgrade
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: job
        image: busybox
`

// podTemplate is a bare Pod as rendered from git: no node, service account or
// token volume, which the scheduler and admission add in a real cluster.
const podTemplate = `apiVersion: v1
kind: Pod
metadata:
  name: debug-pod
  labels:
    rev: %q
spec:
  containers:
  - name: c
    image: busybox
    volumeMounts:
    - name: cache
      mountPath: /cache
  volumes:
  - name: cache
    emptyDir: {}
`

// preserveFieldsResources returns the bundle contents used to check that
// fields listed in helm.preserveOnReplace are kept on forced replaces.
func preserveFieldsResources() map[string][]v1alpha1.BundleResource {
	return map[string][]v1alpha1.BundleResource{
		"pvc-v1":          {pvcResource("1")},
		"pvc-v2":          {pvcResource("2")},
		"pvc-v3":          {pvcResource("3")},
		"pvc-never-ready": {pvcResource("2"), {Name: "deployment.yaml", Content: neverReadyDeployment}},
		"pod-v1":          {podResource("1")},
		"pod-v2":          {podResource("2")},
		"hook-job-v1":     {configMapResource("1"), hookJobResource("1")},
		"hook-job-v2":     {configMapResource("2"), hookJobResource("2")},
		"hook-job-v3":     {configMapResource("3"), hookJobResource("3")},
	}
}

func pvcResource(rev string) v1alpha1.BundleResource {
	return v1alpha1.BundleResource{Name: "pvc.yaml", Content: fmt.Sprintf(pvcTemplate, rev)}
}

func configMapResource(rev string) v1alpha1.BundleResource {
	return v1alpha1.BundleResource{Name: "cm.yaml", Content: fmt.Sprintf(preservedConfigMapTemplate, rev)}
}

func hookJobResource(rev string) v1alpha1.BundleResource {
	return v1alpha1.BundleResource{Name: "job.yaml", Content: fmt.Sprintf(hookJobTemplate, rev)}
}

func podResource(rev string) v1alpha1.BundleResource {
	return v1alpha1.BundleResource{Name: "pod.yaml", Content: fmt.Sprintf(podTemplate, rev)}
}

// Rules from the helm.preserveOnReplace documentation.
var (
	pvcPreserveRule = v1alpha1.PreserveOnReplaceRule{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Paths:      []v1alpha1.PreservedPath{{Path: "/spec/volumeName"}, {Path: "/spec/storageClassName"}},
	}
	jobPreserveRule = v1alpha1.PreserveOnReplaceRule{
		APIVersion: "batch/v1",
		Kind:       "Job",
		Paths: []v1alpha1.PreservedPath{
			{Path: "/spec/selector"},
			{Path: "/spec/template/metadata/labels/controller-uid"},
			{Path: "/spec/template/metadata/labels/batch.kubernetes.io~1controller-uid"},
			{Path: "/spec/template/metadata/labels/job-name"},
			{Path: "/spec/template/metadata/labels/batch.kubernetes.io~1job-name"},
		},
	}
	podPreserveRule = v1alpha1.PreserveOnReplaceRule{
		APIVersion: "v1",
		Kind:       "Pod",
		NameRegex:  "^debug-",
		Paths: []v1alpha1.PreservedPath{
			{Path: "/spec/nodeName"},
			{Path: "/spec/serviceAccountName"},
			{Path: "/spec/volumes", MergeKey: "name"},
			{Path: "/spec/containers/[name]/volumeMounts", MergeKey: "mountPath"},
		},
	}
)

var _ = Describe("Preserving fields set on the server on a forced replace (helm.preserveOnReplace)", func() {
	var env *preserveEnv

	BeforeEach(func() {
		namespace := createNamespace()
		env = &preserveEnv{
			namespace: namespace,
			pvKey:     types.NamespacedName{Name: "pv-" + namespace},
			options: v1alpha1.BundleDeploymentOptions{
				DefaultNamespace: namespace,
				Helm:             &v1alpha1.HelmOptions{},
			},
		}
	})

	When("a bound PVC is updated with helm.force and no helm.preserveOnReplace", func() {
		BeforeEach(func() {
			env.name = "preserve-pvc-force-no-rules"
			env.options.Helm.Force = true
		})

		// The rules are part of the deployment ID, so the controller gives
		// the update a new ID once the rule is added, and the agent retries
		// it although the rejected deployment ID counts as applied.
		It("is rejected by the API server, and applied once the PVC rule is added", func() {
			env.deployBoundPVC()

			env.updateBundleDeployment(deployID("pvc-v2"))
			env.expectRejected()

			By("adding the rule, with the new deployment ID the controller computes")
			env.updateBundleDeployment(func(bd *v1alpha1.BundleDeployment) {
				bd.Spec.DeploymentID = "pvc-v2:with-rules"
				bd.Spec.Options.Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{pvcPreserveRule}
			})
			env.waitForApplied("pvc-v2:with-rules")
			env.expectBoundPVC("2")
		})
	})

	When("a bound PVC is updated with helm.force and the PVC rule", func() {
		BeforeEach(func() {
			env.name = "preserve-pvc-force"
			env.options.Helm.Force = true
			env.options.Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{pvcPreserveRule}
		})

		It("stores the release manifest as rendered, without the fields set on the server", func() {
			env.deployBoundPVC()

			env.updateBundleDeployment(deployID("pvc-v2"))
			env.waitForApplied("pvc-v2")
			env.expectBoundPVC("2")

			manifest := env.releaseManifest()
			Expect(manifest).To(ContainSubstring("rev: \"2\""))
			Expect(manifest).ToNot(ContainSubstring("volumeName"))
			Expect(manifest).ToNot(ContainSubstring("storageClassName"))
		})

		It("keeps applying updates after force is switched off", func() {
			env.deployBoundPVC()

			By("updating with force")
			env.updateBundleDeployment(deployID("pvc-v2"))
			env.waitForApplied("pvc-v2")
			env.expectBoundPVC("2")

			By("updating without force")
			env.updateBundleDeployment(func(bd *v1alpha1.BundleDeployment) {
				bd.Spec.DeploymentID = "pvc-v3"
				bd.Spec.Options.Helm.Force = false
			})
			env.waitForApplied("pvc-v3")
			env.expectBoundPVC("3")
		})
	})

	When("an upgrade of a bound PVC with helm.force, helm.atomic and the PVC rule fails", func() {
		BeforeEach(func() {
			env.name = "preserve-pvc-atomic-force"
			env.options.Helm.Force = true
			env.options.Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{pvcPreserveRule}
		})

		// The release installed before the claim was bound lacks the fields
		// set on the server, and the rollback replaces the claim with it.
		It("rolls back to the release installed before the claim was bound", func() {
			// Without helm.atomic, which makes Helm wait for the claim to
			// be bound.
			env.deployBoundPVC()

			By("updating to a release which never becomes ready")
			env.updateBundleDeployment(func(bd *v1alpha1.BundleDeployment) {
				bd.Spec.DeploymentID = "pvc-never-ready"
				bd.Spec.Options.Helm.Atomic = true
				bd.Spec.Options.Helm.TimeoutSeconds = 3
			})
			Eventually(env.conditionMessages).Should(ContainSubstring("has been rolled back due to rollback-on-failure"))
			Expect(env.conditionMessages(Default)).ToNot(ContainSubstring("an error occurred while rolling back"))
			env.expectBoundPVC("1")
		})
	})

	When("drift on a bound PVC is corrected with correctDrift.force and the PVC rule", func() {
		BeforeEach(func() {
			env.name = "preserve-pvc-correct-drift-force"
			env.correctDrift = &v1alpha1.CorrectDrift{Enabled: true, Force: true}
			env.options.CorrectDrift = env.correctDrift
			env.options.Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{pvcPreserveRule}
		})

		// The rollback replaces the claim with the release manifest stored
		// before it was bound.
		It("corrects drift and keeps the fields set on the server", func() {
			env.deployBoundPVC()

			Eventually(func() error {
				pvc := &corev1.PersistentVolumeClaim{}
				if err := k8sClient.Get(ctx, env.pvcKey(), pvc); err != nil {
					return err
				}
				pvc.Labels["rev"] = "drifted"
				return k8sClient.Update(ctx, pvc)
			}).Should(Succeed())

			env.expectBoundPVC("1")
			Expect(env.conditionMessages(Default)).ToNot(ContainSubstring(immutableSpecError))
		})
	})

	When("a hook Job is run by successive upgrades with helm.force and the Job rule", func() {
		BeforeEach(func() {
			env.name = "preserve-hook-job-force"
			env.options.Helm.Force = true
			env.options.Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{jobPreserveRule}
		})

		// Helm creates hooks with POST, which the rules leave alone, so each
		// run gets a Job of its own.
		It("runs the hook on every upgrade", func() {
			env.createBundleDeployment("hook-job-v1")
			uid := env.completeJob("")
			env.waitForApplied("hook-job-v1")

			for i, id := range []string{"hook-job-v2", "hook-job-v3"} {
				By("upgrading to " + id)
				env.updateBundleDeployment(deployID(id))
				uid = env.completeJob(uid)
				env.waitForApplied(id)

				// A failed upgrade can succeed when Fleet retries it, which
				// also gets the bundle deployment applied, but it leaves a
				// failed revision behind.
				Expect(env.deployedRelease(Default).Labels).To(HaveKeyWithValue("version", strconv.Itoa(i+2)))
			}
		})
	})

	When("a bare Pod is updated with helm.force and the Pod rule", func() {
		BeforeEach(func() {
			env.name = "preserve-pod-force"
			env.options.Helm.Force = true
			env.options.Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{podPreserveRule}
		})

		It("installs over the live Pod and applies updates, keeping the fields set on the server", func() {
			env.createLivePod()

			By("installing")
			env.createBundleDeployment("pod-v1")
			env.waitForApplied("pod-v1")
			env.expectPod("1")

			By("upgrading")
			env.updateBundleDeployment(deployID("pod-v2"))
			env.waitForApplied("pod-v2")
			env.expectPod("2")
		})
	})
})

// preserveEnv holds the state of a helm.preserveOnReplace spec. Specs set
// name, options and correctDrift before creating the BundleDeployment.
type preserveEnv struct {
	namespace string
	name      string
	options   v1alpha1.BundleDeploymentOptions
	// correctDrift is the BundleDeployment's spec.correctDrift, used by
	// drift correction, while options.CorrectDrift is used by upgrades.
	correctDrift *v1alpha1.CorrectDrift
	pvKey        types.NamespacedName
}

func (e *preserveEnv) bdKey() types.NamespacedName {
	return types.NamespacedName{Namespace: clusterNS, Name: e.name}
}

func (e *preserveEnv) jobKey() types.NamespacedName {
	return types.NamespacedName{Namespace: e.namespace, Name: "job"}
}

func (e *preserveEnv) pvcKey() types.NamespacedName {
	return types.NamespacedName{Namespace: e.namespace, Name: boundPVCName}
}

func (e *preserveEnv) podKey() types.NamespacedName {
	return types.NamespacedName{Namespace: e.namespace, Name: "debug-pod"}
}

func (e *preserveEnv) createBundleDeployment(deploymentID string) {
	bd := &v1alpha1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: e.name, Namespace: clusterNS},
		Spec: v1alpha1.BundleDeploymentSpec{
			DeploymentID: deploymentID,
			Options:      e.options,
			CorrectDrift: e.correctDrift,
		},
	}
	Expect(k8sClient.Create(ctx, bd)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), bd))).To(Succeed())
	})
}

func (e *preserveEnv) updateBundleDeployment(update func(*v1alpha1.BundleDeployment)) {
	Eventually(func() error {
		bd := &v1alpha1.BundleDeployment{}
		if err := k8sClient.Get(ctx, e.bdKey(), bd); err != nil {
			return err
		}
		update(bd)
		return k8sClient.Update(ctx, bd)
	}).Should(Succeed())
}

// deployID returns an update for updateBundleDeployment which sets the
// deployment ID.
func deployID(id string) func(*v1alpha1.BundleDeployment) {
	return func(bd *v1alpha1.BundleDeployment) { bd.Spec.DeploymentID = id }
}

// conditionMessages returns the messages of the BundleDeployment's
// conditions, joined.
func (e *preserveEnv) conditionMessages(g Gomega) string {
	bd := &v1alpha1.BundleDeployment{}
	g.Expect(k8sClient.Get(ctx, e.bdKey(), bd)).To(Succeed())
	var msgs []string
	for _, c := range bd.Status.Conditions {
		msgs = append(msgs, c.Message)
	}
	return strings.Join(msgs, "\n")
}

func (e *preserveEnv) getPVC(g Gomega) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{}
	g.Expect(k8sClient.Get(ctx, e.pvcKey(), pvc)).To(Succeed())
	return pvc
}

func (e *preserveEnv) waitForApplied(deploymentID string) {
	Eventually(func(g Gomega) {
		bd := &v1alpha1.BundleDeployment{}
		g.Expect(k8sClient.Get(ctx, e.bdKey(), bd)).To(Succeed())
		// Not checking readiness: a claim is not ready until it is bound.
		g.Expect(bd.Status.AppliedDeploymentID).To(Equal(deploymentID))
	}).Should(Succeed())
}

// expectBoundPVC checks that the claim is at revision rev and still bound to
// its volume.
func (e *preserveEnv) expectBoundPVC(rev string) {
	Eventually(func(g Gomega) {
		pvc := e.getPVC(g)
		g.Expect(pvc.Labels).To(HaveKeyWithValue("rev", rev))
		g.Expect(pvc.Spec.VolumeName).To(Equal(e.pvKey.Name))
		g.Expect(pvc.Spec.StorageClassName).To(Equal(ptr.To(boundPVCStorageClass)))
	}).Should(Succeed())
}

// expectRejected waits for the update to fail with the API server's immutable
// spec error and checks the claim was left as it was.
func (e *preserveEnv) expectRejected() {
	Eventually(e.conditionMessages).Should(ContainSubstring(immutableSpecError))
	Consistently(func(g Gomega) {
		g.Expect(e.getPVC(g).Labels).To(HaveKeyWithValue("rev", "1"))
	}, "3s").Should(Succeed())
	e.expectBoundPVC("1")
}

// bindPVC does what the PV controller and the DefaultStorageClass admission
// plugin do in a real cluster, which envtest does not run: it binds the claim
// to a new volume and sets its storage class.
func (e *preserveEnv) bindPVC() {
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: e.pvKey.Name},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity:         corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			StorageClassName: boundPVCStorageClass,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: "/tmp/" + e.pvKey.Name},
			},
			ClaimRef: &corev1.ObjectReference{Namespace: e.namespace, Name: boundPVCName},
		},
	}
	Expect(k8sClient.Create(ctx, pv)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), pv))).To(Succeed())
	})

	var pvc *corev1.PersistentVolumeClaim
	Eventually(func(g Gomega) { pvc = e.getPVC(g) }).Should(Succeed())
	pvc.Spec.VolumeName = e.pvKey.Name
	pvc.Spec.StorageClassName = ptr.To(boundPVCStorageClass)
	Expect(k8sClient.Update(ctx, pvc)).To(Succeed())
	pvc.Status.Phase = corev1.ClaimBound
	Expect(k8sClient.Status().Update(ctx, pvc)).To(Succeed())
}

// deployBoundPVC deploys pvc-v1 and binds the claim.
func (e *preserveEnv) deployBoundPVC() {
	e.createBundleDeployment("pvc-v1")
	e.waitForApplied("pvc-v1")
	e.bindPVC()
}

// completeJob waits for a Job other than the one with UID previous and marks
// it as complete, as the Job controller would after its pod succeeded. It
// returns the Job's UID.
func (e *preserveEnv) completeJob(previous types.UID) types.UID {
	job := &batchv1.Job{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, e.jobKey(), job)).To(Succeed())
		g.Expect(job.UID).ToNot(Equal(previous))
	}).Should(Succeed())

	now := metav1.Now()
	job.Status = batchv1.JobStatus{
		StartTime:      &now,
		CompletionTime: &now,
		Succeeded:      1,
		Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobSuccessCriteriaMet, Status: corev1.ConditionTrue, LastTransitionTime: now},
			{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: now},
		},
	}
	Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())
	return job.UID
}

// deployedRelease returns the Secret in which Helm stores the deployed
// release in the test namespace.
func (e *preserveEnv) deployedRelease(g Gomega) corev1.Secret {
	secrets := &corev1.SecretList{}
	g.Expect(k8sClient.List(ctx, secrets, client.InNamespace(e.namespace),
		client.MatchingLabels{"owner": "helm", "status": "deployed"})).To(Succeed())
	g.Expect(secrets.Items).To(HaveLen(1))
	return secrets.Items[0]
}

// releaseManifest returns the manifest of the deployed Helm release in the
// test namespace, as stored by Helm.
func (e *preserveEnv) releaseManifest() string {
	secret := e.deployedRelease(Default)
	data, err := base64.StdEncoding.DecodeString(string(secret.Data["release"]))
	Expect(err).ToNot(HaveOccurred())
	r, err := gzip.NewReader(bytes.NewReader(data))
	Expect(err).ToNot(HaveOccurred())
	data, err = io.ReadAll(r)
	Expect(err).ToNot(HaveOccurred())

	var rel struct {
		Manifest string `json:"manifest"`
	}
	Expect(json.Unmarshal(data, &rel)).To(Succeed())
	Expect(rel.Manifest).ToNot(BeEmpty())
	return rel.Manifest
}

// createLivePod creates the Pod as a real cluster would have it once Helm
// created it from pod-v1: envtest runs neither the scheduler nor the
// ServiceAccount admission plugin, and a Pod's volumes cannot be changed after
// its creation. The Helm ownership metadata lets the install adopt it, which
// replaces it with a PUT.
func (e *preserveEnv) createLivePod() {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "debug-pod",
			Namespace: e.namespace,
			Labels:    map[string]string{"rev": "0", "app.kubernetes.io/managed-by": "Helm"},
			Annotations: map[string]string{
				"meta.helm.sh/release-name":      e.name,
				"meta.helm.sh/release-namespace": e.namespace,
			},
		},
		Spec: corev1.PodSpec{
			NodeName:           "node-1",
			ServiceAccountName: "workload",
			Containers: []corev1.Container{{
				Name:  "c",
				Image: "busybox",
				VolumeMounts: []corev1.VolumeMount{
					{Name: "cache", MountPath: "/cache"},
					{Name: "kube-api-access", MountPath: "/var/run/secrets/kubernetes.io/serviceaccount", ReadOnly: true},
				},
			}},
			Volumes: []corev1.Volume{
				{Name: "cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "kube-api-access", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token"}}},
				}}},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
}

func (e *preserveEnv) expectPod(rev string) {
	Eventually(func(g Gomega) {
		pod := &corev1.Pod{}
		g.Expect(k8sClient.Get(ctx, e.podKey(), pod)).To(Succeed())
		g.Expect(pod.Labels).To(HaveKeyWithValue("rev", rev))
		g.Expect(pod.Spec.NodeName).To(Equal("node-1"))
		g.Expect(pod.Spec.ServiceAccountName).To(Equal("workload"))
		g.Expect(pod.Spec.Volumes).To(HaveLen(2))
		g.Expect(pod.Spec.Containers[0].VolumeMounts).To(HaveLen(2))
	}).Should(Succeed())
}
