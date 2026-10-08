package singlecluster_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/internal/cmd/controller/bundleevents"
	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// controllerNamespace is where the fleet-controller config map lives.
	controllerNamespace = "cattle-fleet-system"

	// noEventsWindow is how long a test waits to be sure that no further
	// event is created. It is longer than the configured debounce, so that an
	// event which was going to be created has been.
	noEventsWindow = 5 * time.Second

	// debounce is the debounce the specs configure. It is kept short so that
	// the noEventsWindow, which has to outlast it, keeps the run brief.
	debounce = "2s"

	// configNotAdoptedHint explains the likeliest cause of a spec observing
	// the events of the options a previous spec configured.
	configNotAdoptedHint = "the controller may not have adopted the deploymentEvents config set by this spec: " +
		"its fleet.cattle.io/version annotation has to match the controller version, " +
		"and the reload has to happen before the bundle first fails (see setDeploymentEvents)"
)

// Bundles reporting their deployment state as Kubernetes events. The tests
// create bundles directly, which fail to deploy because one of their resources
// is of a kind the cluster does not know, and recover once that resource is
// replaced.
var _ = Describe("Bundle deployment events", Serial, Ordered, func() {
	var (
		bundleName string
		bundle     *fleet.Bundle
	)

	BeforeAll(func() {
		var cm corev1.ConfigMap
		Expect(clientUpstream.Get(context.TODO(), types.NamespacedName{
			Namespace: controllerNamespace,
			Name:      config.ManagerConfigName,
		}, &cm)).To(Succeed())
		originalConfig := cm.Data[config.Key]

		// Registered only once the config has been read, so that a failed
		// read cannot replace the config with an empty one, which would
		// leave the controller with its defaults for the rest of the suite.
		DeferCleanup(func() {
			updateControllerConfig(func(string) (string, error) {
				return originalConfig, nil
			})
		})
	})

	BeforeEach(func() {
		bundleName = testenv.AddRandomSuffix("events", rand.NewSource(time.Now().UnixNano()))
	})

	JustBeforeEach(func() {
		bundle = &fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bundleName,
				Namespace: env.Namespace,
			},
			Spec: fleet.BundleSpec{
				Resources: []fleet.BundleResource{unknownKind(bundleName, "Unknown")},
				Targets: []fleet.BundleTarget{
					{ClusterSelector: &metav1.LabelSelector{}},
				},
			},
		}
		Expect(clientUpstream.Create(context.TODO(), bundle)).To(Succeed())

		created := bundle
		DeferCleanup(func() {
			_ = clientUpstream.Delete(context.TODO(), created)
		})
	})

	When("events are disabled", func() {
		BeforeEach(func() {
			setDeploymentEvents(map[string]any{"enabled": false})
		})

		It("does not report a failing bundle", func() {
			bundleShouldBeFailing(bundleName)

			Consistently(func(g Gomega) {
				g.Expect(eventsFor(g, bundle)).To(BeEmpty())
			}, noEventsWindow).Should(Succeed(), configNotAdoptedHint)
		})
	})

	When("per-deployment events are disabled", func() {
		BeforeEach(func() {
			setDeploymentEvents(map[string]any{"debounce": debounce, "minInterval": "1s"})
		})

		It("reports failures, new causes and recoveries on the bundle only", func() {
			By("reporting the failure once")
			Eventually(func(g Gomega) {
				events := eventsFor(g, bundle)
				g.Expect(reasons(events)).To(Equal([]string{bundleevents.ReasonBundleDeployFailed}))
				g.Expect(events[0].Type).To(Equal(corev1.EventTypeWarning))
				g.Expect(events[0].Note).To(ContainSubstring("1/1 bundle deployments failing"))
			}).Should(Succeed(), configNotAdoptedHint)

			By("not repeating the event while the bundle keeps failing the same way, nor reporting the bundle deployment")
			bd := bundleDeploymentFor(bundleName)
			Consistently(func(g Gomega) {
				g.Expect(reasons(eventsFor(g, bundle))).To(Equal([]string{bundleevents.ReasonBundleDeployFailed}))
				g.Expect(eventsFor(g, bd)).To(BeEmpty())
			}, noEventsWindow).Should(Succeed())

			By("reporting a new cause of failure")
			updateResources(bundle, unknownKind(bundleName, "OtherUnknown"))
			Eventually(func(g Gomega) {
				g.Expect(reasons(eventsFor(g, bundle))).To(Equal([]string{
					bundleevents.ReasonBundleDeployFailed,
					bundleevents.ReasonBundleDeployFailed,
				}))
			}).Should(Succeed())

			By("reporting the recovery once")
			updateResources(bundle, validConfigMap(bundleName))
			Eventually(func(g Gomega) {
				events := eventsFor(g, bundle)
				g.Expect(reasons(events)).To(Equal([]string{
					bundleevents.ReasonBundleDeployFailed,
					bundleevents.ReasonBundleDeployFailed,
					bundleevents.ReasonBundleReady,
				}))
				g.Expect(events[2].Type).To(Equal(corev1.EventTypeNormal))
				g.Expect(events[2].Note).To(Equal("1/1 bundle deployments ready"))
			}).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(eventsFor(g, bundle)).To(HaveLen(3))
			}, noEventsWindow).Should(Succeed())

			By("reporting a failure which comes back")
			updateResources(bundle, unknownKind(bundleName, "Unknown"))
			Eventually(func(g Gomega) {
				g.Expect(reasons(eventsFor(g, bundle))).To(Equal([]string{
					bundleevents.ReasonBundleDeployFailed,
					bundleevents.ReasonBundleDeployFailed,
					bundleevents.ReasonBundleReady,
					bundleevents.ReasonBundleDeployFailed,
				}))
			}).Should(Succeed())

			By("still not reporting the bundle deployment")
			Consistently(func(g Gomega) {
				g.Expect(eventsFor(g, bundle)).To(HaveLen(4))
				g.Expect(eventsFor(g, bd)).To(BeEmpty())
			}, noEventsWindow).Should(Succeed())
		})
	})

	When("per-deployment events are enabled", func() {
		BeforeEach(func() {
			setDeploymentEvents(map[string]any{"debounce": debounce, "minInterval": "1s", "perDeployment": true})
		})

		It("reports failures and recoveries on the bundle and on the bundle deployment", func() {
			By("reporting the failure on the bundle")
			Eventually(func(g Gomega) {
				g.Expect(reasons(eventsFor(g, bundle))).To(Equal([]string{bundleevents.ReasonBundleDeployFailed}))
			}).Should(Succeed())

			By("reporting the failure on the bundle deployment, in the namespace of its cluster")
			bd := bundleDeploymentFor(bundleName)
			Eventually(func(g Gomega) {
				events := eventsFor(g, bd)
				g.Expect(reasons(events)).To(Equal([]string{bundleevents.ReasonBundleDeploymentFailed}))
				g.Expect(events[0].Namespace).To(Equal(bd.Namespace))
				g.Expect(events[0].Type).To(Equal(corev1.EventTypeWarning))
			}).Should(Succeed(), configNotAdoptedHint)

			By("not repeating either event while the bundle keeps failing the same way")
			Consistently(func(g Gomega) {
				g.Expect(eventsFor(g, bundle)).To(HaveLen(1))
				g.Expect(eventsFor(g, bd)).To(HaveLen(1))
			}, noEventsWindow).Should(Succeed())

			By("reporting the recovery on both")
			updateResources(bundle, validConfigMap(bundleName))
			Eventually(func(g Gomega) {
				g.Expect(reasons(eventsFor(g, bundle))).To(Equal([]string{
					bundleevents.ReasonBundleDeployFailed,
					bundleevents.ReasonBundleReady,
				}))
				g.Expect(reasons(eventsFor(g, bd))).To(Equal([]string{
					bundleevents.ReasonBundleDeploymentFailed,
					bundleevents.ReasonBundleDeploymentReady,
				}))
			}).Should(Succeed())
		})
	})
})

// unknownKind is a resource of a kind the cluster does not know, which makes
// the bundle fail to deploy. Different kinds are different causes of failure.
func unknownKind(name, kind string) fleet.BundleResource {
	return fleet.BundleResource{
		Name: "unknown.yaml",
		Content: fmt.Sprintf(`apiVersion: e2e.fleet.cattle.io/v1
kind: %s
metadata:
  name: %s
  namespace: default
`, kind, name),
	}
}

// validConfigMap is a resource which deploys and is ready right away.
func validConfigMap(name string) fleet.BundleResource {
	return fleet.BundleResource{
		Name: "configmap.yaml",
		Content: fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  ready: "true"
`, name),
	}
}

// updateResources replaces the resources of the bundle, retrying on conflicts
// with the status updates of the controller.
func updateResources(bundle *fleet.Bundle, resources ...fleet.BundleResource) {
	Eventually(func() error {
		var cur fleet.Bundle
		if err := clientUpstream.Get(context.TODO(), client.ObjectKeyFromObject(bundle), &cur); err != nil {
			return err
		}
		cur.Spec.Resources = resources
		return clientUpstream.Update(context.TODO(), &cur)
	}).Should(Succeed())
}

// setDeploymentEvents replaces the deploymentEvents block of the controller
// config. The controller reads it live, so no restart is needed.
//
// Nothing waits for the controller to adopt the change: the test relies on the
// config reconciler picking it up, which takes well under a second, before the
// agent has deployed the bundle the test creates next, which takes seconds. If
// that ever stops holding, a spec fails with the hint in configNotAdoptedHint:
// the emitter only reports the transition into a failure, so a failure first
// observed with the previous options is not reported later.
func setDeploymentEvents(events map[string]any) {
	updateControllerConfig(func(data string) (string, error) {
		cfg := map[string]any{}
		if data != "" {
			if err := json.Unmarshal([]byte(data), &cfg); err != nil {
				return "", err
			}
		}
		cfg["deploymentEvents"] = events

		out, err := json.Marshal(cfg)
		return string(out), err
	})
}

// updateControllerConfig changes the config held by the fleet-controller config
// map, keeping its version annotation. The running controller only adopts the
// change if that annotation matches its own version, which is the case for the
// dev and CI builds, where both are "dev".
func updateControllerConfig(change func(data string) (string, error)) {
	Eventually(func() error {
		var cm corev1.ConfigMap
		if err := clientUpstream.Get(context.TODO(), types.NamespacedName{
			Namespace: controllerNamespace,
			Name:      config.ManagerConfigName,
		}, &cm); err != nil {
			return err
		}

		data, err := change(cm.Data[config.Key])
		if err != nil {
			return err
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[config.Key] = data

		return clientUpstream.Update(context.TODO(), &cm)
	}).Should(Succeed())
}

// bundleShouldBeFailing waits for the bundle to report a deployment which
// failed to apply.
func bundleShouldBeFailing(name string) {
	Eventually(func(g Gomega) {
		var b fleet.Bundle
		g.Expect(clientUpstream.Get(context.TODO(), types.NamespacedName{Namespace: env.Namespace, Name: name}, &b)).To(Succeed())
		g.Expect(b.Status.Summary.ErrApplied).To(Equal(1))
	}).Should(Succeed())
}

// bundleDeploymentFor returns the single bundle deployment of a bundle.
func bundleDeploymentFor(bundleName string) *fleet.BundleDeployment {
	var bd fleet.BundleDeployment
	Eventually(func(g Gomega) {
		var list fleet.BundleDeploymentList
		g.Expect(clientUpstream.List(context.TODO(), &list, client.MatchingLabels{
			fleet.BundleLabel:          bundleName,
			fleet.BundleNamespaceLabel: env.Namespace,
		})).To(Succeed())
		g.Expect(list.Items).To(HaveLen(1))
		bd = list.Items[0]
	}).Should(Succeed())

	return &bd
}

// eventsFor returns the deployment state events about an object, oldest first.
func eventsFor(g Gomega, obj client.Object) []eventsv1.Event {
	var list eventsv1.EventList
	g.Expect(clientUpstream.List(context.TODO(), &list, client.InNamespace(obj.GetNamespace()))).To(Succeed())

	var events []eventsv1.Event
	for _, ev := range list.Items {
		if ev.Regarding.UID == obj.GetUID() && ev.Action == "Deploy" {
			events = append(events, ev)
		}
	}
	slices.SortFunc(events, func(a, b eventsv1.Event) int {
		return a.EventTime.Compare(b.EventTime.Time)
	})

	return events
}

func reasons(events []eventsv1.Event) []string {
	r := make([]string, 0, len(events))
	for _, ev := range events {
		r = append(r, ev.Reason)
	}

	return r
}
