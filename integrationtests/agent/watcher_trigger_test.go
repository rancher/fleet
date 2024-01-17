package agent

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/pointer"

	"github.com/rancher/fleet/internal/cmd/agent/trigger"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Watches for deployed resources", Ordered, func() {

	var (
		env    *specEnv
		triggr *trigger.Trigger
	)

	BeforeAll(func() {
		env = specEnvs["watchertrigger"]
		triggr = trigger.New(ctx, env.k8sClient.RESTMapper(), dynamic.NewForConfigOrDie(cfg))
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.namespace}})).ToNot(HaveOccurred())
		})
	})

	registerResource := func(key string, objs ...runtime.Object) *int {
		var count int
		Expect(triggr.OnChange(key, env.namespace, func() {
			count++
		}, objs...)).ToNot(HaveOccurred())
		return &count
	}

	When("watching a deployed configmap", Ordered, func() {
		createConfigMap := func() *corev1.ConfigMap {
			cm := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-cm-",
					Namespace:    env.namespace,
				},
			}

			err := env.k8sClient.Create(ctx, &cm)
			Expect(err).ToNot(HaveOccurred())
			Expect(cm.UID).ToNot(BeEmpty())
			return &cm
		}

		var triggerCount *int
		var cm *corev1.ConfigMap
		BeforeEach(func() {
			cm = createConfigMap()
			triggerCount = registerResource(env.namespace+"/test-configmap", cm)
			DeferCleanup(func() {
				Expect(
					client.IgnoreNotFound(env.k8sClient.Delete(ctx, cm))).
					NotTo(HaveOccurred())
			})
		})
		It("is not initially triggered", func() {
			Consistently(func() int {
				return *triggerCount
			}).Should(Equal(0))
		})
		It("is triggered on deletion", func() {
			Expect(env.k8sClient.Delete(ctx, cm)).ToNot(HaveOccurred())
			Eventually(func() int {
				return *triggerCount
			}).WithPolling(100 * time.Millisecond).MustPassRepeatedly(3).
				Should(Equal(1))
		})
		It("is always triggered when modified", func() {
			cm.Data = map[string]string{"foo": "bar"}
			Expect(env.k8sClient.Update(ctx, cm)).ToNot(HaveOccurred())
			Eventually(func() int {
				return *triggerCount
			}).WithPolling(100 * time.Millisecond).MustPassRepeatedly(3).
				Should(Equal(1))

			cm.Data = map[string]string{"bar": "baz"}
			Expect(env.k8sClient.Update(ctx, cm)).ToNot(HaveOccurred())
			Eventually(func() int {
				return *triggerCount
			}).WithPolling(100 * time.Millisecond).MustPassRepeatedly(3).
				Should(Equal(2))
		})
	})
	When("watching a deployed deployment", Ordered, func() {
		createDeployment := func() *appsv1.Deployment {
			deploy := appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-deploy-",
					Namespace:    env.namespace,
					Generation:   1,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(0),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "test",
						},
					},
					Paused: true,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "test",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "test",
									Image: "test-image",
								},
							},
						},
					},
				},
			}

			err := env.k8sClient.Create(ctx, &deploy)
			Expect(err).ToNot(HaveOccurred())
			Expect(deploy.UID).ToNot(BeEmpty())
			// envtest does not return a complete object, which is needed for the trigger to work
			deploy.TypeMeta = metav1.TypeMeta{
				Kind:       "Deployment",
				APIVersion: "apps/v1",
			}
			return &deploy
		}
		var triggerCount *int
		var deploy *appsv1.Deployment

		BeforeEach(func() {
			deploy = createDeployment()
			triggerCount = registerResource(env.namespace+"/test-deploy", deploy)
			DeferCleanup(func() {
				Expect(
					client.IgnoreNotFound(env.k8sClient.Delete(ctx, deploy))).
					NotTo(HaveOccurred())
			})
		})
		It("is not initially triggered", func() {
			Consistently(func() int {
				return *triggerCount
			}).Should(Equal(0))
		})
		It("is triggered on deletion", func() {
			Expect(env.k8sClient.Delete(ctx, deploy)).ToNot(HaveOccurred())
			Eventually(func() int {
				return *triggerCount
			}).WithPolling(100 * time.Millisecond).MustPassRepeatedly(3).
				Should(Equal(1))
		})
		It("is not triggered on status updates", func() {
			deploy.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:               appsv1.DeploymentAvailable,
					Status:             corev1.ConditionFalse,
					LastUpdateTime:     metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Message:            "tests",
					Reason:             "tests",
				},
			}
			Expect(env.k8sClient.Status().Update(ctx, deploy)).ToNot(HaveOccurred())
			Consistently(func() int {
				return *triggerCount
			}).Should(Equal(0))
		})
		It("is triggered on Spec changes", func() {
			for i := 1; i <= 5; i++ {
				deploy.Spec.Replicas = pointer.Int32(int32(i))
				Expect(env.k8sClient.Update(ctx, deploy)).ToNot(HaveOccurred())
				Eventually(func() int {
					return *triggerCount
				}).WithPolling(100 * time.Millisecond).MustPassRepeatedly(3).
					Should(Equal(i))
			}
		})
	})
})
