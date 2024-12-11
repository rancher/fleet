package controller

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
)

var letters = []rune("abcdefghijklmnopqrstuvwxyz")

const (
	maxLabelsLength        = 5
	maxGenericStringLength = 10
	authUsername           = "superuser"
	authPassword           = "superpassword"
	helmRepoIndex          = `apiVersion: v1
entries:
  alpine:
    - created: 2016-10-06T16:23:20.499814565-06:00
      description: Deploy a basic Alpine Linux pod
      digest: 99c76e403d752c84ead610644d4b1c2f2b453a74b921f422b9dcb8a7c8b559cd
      home: https://helm.sh/helm
      name: alpine
      sources:
      - https://github.com/helm/helm
      urls:
      - https://technosophos.github.io/tscharts/alpine-0.2.0.tgz
      version: 0.2.0
    - created: 2016-10-06T16:23:20.499543808-06:00
      description: Deploy a basic Alpine Linux pod
      digest: 515c58e5f79d8b2913a10cb400ebb6fa9c77fe813287afbacf1a0b897cd78727
      home: https://helm.sh/helm
      name: alpine
      sources:
      - https://github.com/helm/helm
      urls:
      - https://technosophos.github.io/tscharts/alpine-0.1.0.tgz
      version: 0.1.0
  nginx:
    - created: 2016-10-06T16:23:20.499543808-06:00
      description: Create a basic nginx HTTP server
      digest: aaff4545f79d8b2913a10cb400ebb6fa9c77fe813287afbacf1a0b897cdffffff
      home: https://helm.sh/helm
      name: nginx
      sources:
      - https://github.com/helm/charts
      urls:
      - https://technosophos.github.io/tscharts/nginx-1.1.0.tgz
      version: 1.1.0
generated: 2016-10-06T16:23:20.499029981-06:00`
)

func randBool() bool {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(2) == 1
}

func randString() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]rune, maxGenericStringLength)
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
	}
	return string(b)
}

func randStringSlice() []string {
	n := rand.Intn(maxLabelsLength)
	r := make([]string, n)
	for i := range r {
		r[i] = randString()
	}
	return r
}

func randInterfaceMap() map[string]interface{} {
	nbItems := rand.Intn(maxLabelsLength)
	items := make(map[string]interface{})
	for range nbItems {
		items[randString()] = randString()
	}
	return items
}

func randStringMap() map[string]string {
	m := randInterfaceMap()
	labels := make(map[string]string)
	for k, v := range m {
		s, ok := v.(string)
		if ok {
			labels[k] = s
		}
	}
	return labels
}

func randHelmOptions() *fleet.HelmOptions {
	// we always have helm options in HelmApp resources
	h := &fleet.HelmOptions{
		Chart:                   randString(),
		Repo:                    randString(),
		ReleaseName:             randString(),
		Version:                 randString(), // return also semver version?
		TimeoutSeconds:          rand.Intn(3),
		Values:                  &fleet.GenericMap{Data: randInterfaceMap()},
		Force:                   randBool(),
		TakeOwnership:           randBool(),
		MaxHistory:              rand.Intn(4),
		ValuesFiles:             randStringSlice(),
		WaitForJobs:             randBool(),
		Atomic:                  randBool(),
		DisablePreProcess:       randBool(),
		DisableDNS:              randBool(),
		SkipSchemaValidation:    randBool(),
		DisableDependencyUpdate: randBool(),
	}

	return h
}

func randKustomizeOptions() *fleet.KustomizeOptions {
	if randBool() {
		return nil
	}
	o := &fleet.KustomizeOptions{}
	o.Dir = randString()
	return o
}

func randBundleDeploymentOptions() fleet.BundleDeploymentOptions {
	o := fleet.BundleDeploymentOptions{
		DefaultNamespace: randString(),
		TargetNamespace:  randString(),
		Kustomize:        randKustomizeOptions(),
		Helm:             randHelmOptions(),
		CorrectDrift:     randCorrectDrift(),
		ServiceAccount:   randString(),
	}
	return o
}

func randCorrectDrift() *fleet.CorrectDrift {
	if randBool() {
		return nil
	}
	r := &fleet.CorrectDrift{
		Enabled:         randBool(),
		Force:           randBool(),
		KeepFailHistory: randBool(),
	}

	return r
}

func getRandomHelmAppWithTargets(name string, t []fleet.BundleTarget) fleet.HelmApp {
	namespace = testenv.NewNamespaceName(
		name,
		rand.New(rand.NewSource(time.Now().UnixNano())),
	)
	h := fleet.HelmApp{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		// add a few random values
		Spec: fleet.HelmAppSpec{
			Labels: randStringMap(),
			BundleSpec: fleet.BundleSpec{
				BundleDeploymentOptions: randBundleDeploymentOptions(),
			},
			HelmSecretName:        randString(),
			InsecureSkipTLSverify: randBool(),
		},
	}

	h.Spec.Targets = t

	return h
}

// compareBundleAndHelmAppSpecs compares the part that it is expected to be equal
// between a Bundle's spec and a HelmApp's spec.
func compareBundleAndHelmAppSpecs(g Gomega, bundle fleet.BundleSpec, helmapp fleet.BundleSpec) {
	g.Expect(bundle.BundleDeploymentOptions).To(Equal(helmapp.BundleDeploymentOptions))
	g.Expect(bundle.Paused).To(Equal(helmapp.Paused))
	g.Expect(bundle.RolloutStrategy).To(Equal(helmapp.RolloutStrategy))
	g.Expect(bundle.Resources).To(Equal(helmapp.Resources))
	g.Expect(bundle.Targets).To(Equal(helmapp.Targets))
	g.Expect(bundle.TargetRestrictions).To(Equal(helmapp.TargetRestrictions))
	g.Expect(bundle.DependsOn).To(Equal(helmapp.DependsOn))
}

// checkBundleIsAsExpected verifies that the bundle is a valid bundle created after
// the given HelmApp resource.
func checkBundleIsAsExpected(g Gomega, bundle fleet.Bundle, helmapp fleet.HelmApp, expectedTargets []v1alpha1.BundleTarget) {
	g.Expect(bundle.Name).To(Equal(helmapp.Name))
	g.Expect(bundle.Namespace).To(Equal(helmapp.Namespace))
	// the bundle should have the same labels as the helmapp resource
	// plus the fleet.HelmAppLabel containing the name of the helmapp
	lbls := make(map[string]string)
	for k, v := range helmapp.Spec.Labels {
		lbls[k] = v
	}
	lbls = labels.Merge(lbls, map[string]string{
		fleet.HelmAppLabel: helmapp.Name,
	})
	g.Expect(bundle.Labels).To(Equal(lbls))

	g.Expect(bundle.Spec.Resources).To(BeNil())
	g.Expect(bundle.Spec.HelmAppOptions).ToNot(BeNil())
	g.Expect(bundle.Spec.HelmAppOptions.SecretName).To(Equal(helmapp.Spec.HelmSecretName))
	g.Expect(bundle.Spec.HelmAppOptions.InsecureSkipTLSverify).To(Equal(helmapp.Spec.InsecureSkipTLSverify))

	g.Expect(bundle.Spec.Targets).To(Equal(expectedTargets))

	// now that the bundle spec has been checked we assign the helmapp spec targets
	// so it is easier to check the whole spec. (They should be identical except for the
	// targets)
	bundle.Spec.Targets = helmapp.Spec.Targets

	compareBundleAndHelmAppSpecs(g, bundle.Spec, helmapp.Spec.BundleSpec)

	// the bundle controller should add the finalizer
	g.Expect(controllerutil.ContainsFinalizer(&bundle, finalize.BundleFinalizer)).To(BeTrue())
}

func updateHelmApp(helmapp fleet.HelmApp) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var helmAppFromCluster fleet.HelmApp
		err := k8sClient.Get(ctx, types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}, &helmAppFromCluster)
		if err != nil {
			return err
		}
		helmAppFromCluster.Spec = helmapp.Spec
		return k8sClient.Update(ctx, &helmAppFromCluster)
	})
}

func getCondition(fllethelm *fleet.HelmApp, condType string) (genericcondition.GenericCondition, bool) {
	for _, cond := range fllethelm.Status.Conditions {
		if cond.Type == condType {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func checkConditionContains(g Gomega, fllethelm *fleet.HelmApp, condType string, status corev1.ConditionStatus, message string) {
	cond, found := getCondition(fllethelm, condType)
	g.Expect(found).To(BeTrue())
	g.Expect(cond.Type).To(Equal(condType))
	g.Expect(cond.Status).To(Equal(status))
	g.Expect(cond.Message).To(ContainSubstring(message))
}

func newTLSServerWithAuth() *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if ok {
			usernameHash := sha256.Sum256([]byte(username))
			passwordHash := sha256.Sum256([]byte(password))
			expectedUsernameHash := sha256.Sum256([]byte(authUsername))
			expectedPasswordHash := sha256.Sum256([]byte(authUsername))

			usernameMatch := (subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1)
			passwordMatch := (subtle.ConstantTimeCompare(passwordHash[:], expectedPasswordHash[:]) == 1)

			if usernameMatch && passwordMatch {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, helmRepoIndex)
			}
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	return srv
}

func getNewCustomTLSServer(handler http.Handler) (*httptest.Server, error) {
	ts := httptest.NewUnstartedServer(handler)
	serverCert, err := os.ReadFile("assets/server.crt")
	if err != nil {
		return nil, err
	}
	serverKey, err := os.ReadFile("assets/server.key")
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(serverCert, serverKey)
	if err != nil {
		return nil, err
	}
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	return ts, nil
}

var _ = Describe("HelmOps controller", func() {
	When("a new HelmApp is created", func() {
		var helmapp fleet.HelmApp
		var targets []fleet.BundleTarget
		var doAfterNamespaceCreated func()
		JustBeforeEach(func() {
			os.Setenv("EXPERIMENTAL_HELM_OPS", "true")
			nsSpec := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err := k8sClient.Create(ctx, nsSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(k8sClient.Create(ctx, &helmapp)).ToNot(HaveOccurred())
			if doAfterNamespaceCreated != nil {
				doAfterNamespaceCreated()
			}

			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, nsSpec)).ToNot(HaveOccurred())
				_ = k8sClient.Delete(ctx, &helmapp)
			})
		})
		When("targets is empty", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-empty", targets)
			})

			It("creates a bundle with the expected spec and default target", func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())
			})

			It("adds the expected finalizer to the HelmApp resource", func() {
				Eventually(func(g Gomega) {
					fh := &fleet.HelmApp{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, fh)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(controllerutil.ContainsFinalizer(fh, finalize.HelmAppFinalizer)).To(BeTrue())
				}).Should(Succeed())
			})
		})

		When("helmapp is updated", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-updated", targets)
			})

			It("updates the bundle with the expected content", func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())

				// update the HelmApp spec
				helmapp.Spec.Helm.Chart = "superchart"

				err := updateHelmApp(helmapp)
				Expect(err).ToNot(HaveOccurred())

				// Bundle should be updated
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					checkBundleIsAsExpected(g, *bundle, helmapp, t)

					// make this check explicit
					g.Expect(bundle.Spec.Helm.Chart).To(Equal("superchart"))
				}).Should(Succeed())
			})
		})

		When("targets is not empty", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{
					{
						Name:         "one",
						ClusterGroup: "oneGroup",
					},
					{
						Name:         "two",
						ClusterGroup: "twoGroup",
					},
				}
				helmapp = getRandomHelmAppWithTargets("test-not-empty", targets)
			})

			It("creates a bundle with the expected spec and the original targets", func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					checkBundleIsAsExpected(g, *bundle, helmapp, targets)
				}).Should(Succeed())
			})
		})

		When("helm chart is empty", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-empty", targets)
				// no chart is defined
				helmapp.Spec.Helm.Chart = ""
			})

			It("does not create a bundle", func() {
				Consistently(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(BeNil())
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), err)
				}, 5*time.Second, time.Second).Should(Succeed())
			})
		})

		When("helmapp is added and then deleted", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-add-delete", targets)
			})

			It("creates and deletes the bundle", func() {
				// bundle should be initially created
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())

				// delete the helmapp resource
				err := k8sClient.Delete(ctx, &helmapp)
				Expect(err).ShouldNot(HaveOccurred())

				// eventually the bundle should be gone as well
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(BeNil())
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), err)
				}).Should(Succeed())

				// and the helmapp should be gone too (finalizer is deleted)
				Eventually(func(g Gomega) {
					fh := &fleet.HelmApp{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, fh)
					g.Expect(err).ToNot(BeNil())
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), err)
				}).Should(Succeed())
			})
		})

		Context("version is not specified", func() {
			var version string
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-no-version", targets)

				// version is empty
				helmapp.Spec.Helm.Version = version
				// reset secret, no auth is required
				helmapp.Spec.HelmSecretName = ""

				svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, helmRepoIndex)
				}))
				DeferCleanup(func() {
					svr.Close()
				})

				// set the url to the httptest server
				helmapp.Spec.Helm.Repo = svr.URL
				helmapp.Spec.Helm.Chart = "alpine"
			})

			bundleCreatedWithLatestVersion := func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					// the original helmapp has no version defined.
					// it should download version 0.2.0 as it is the
					// latest in the test helm index.html
					// set it here so the check passes and confirms
					// the version obtained was 0.2.0
					helmapp.Spec.Helm.Version = "0.2.0"
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())
			}

			usesVersionSpecified := func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					// the original helmapp has no version defined.
					// it should download version 0.2.0 as it is the
					// latest in the test helm index.html
					// set it here so the check passes and confirms
					// the version obtained was 0.2.0
					helmapp.Spec.Helm.Version = "0.2.0"
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())

				// update the HelmApp spec to use version 0.1.0
				helmapp.Spec.Helm.Version = "0.1.0"

				err := updateHelmApp(helmapp)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					// the original helmapp has no version defined.
					// it should download version 0.1.0 as it is
					// what we specified
					helmapp.Spec.Helm.Version = "0.1.0"
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())
			}

			When("version is empty", func() {
				BeforeEach(func() {
					version = ""
				})
				It("creates a bundle with the latest version it got from the index", bundleCreatedWithLatestVersion)
				It("uses the version specified if later the user sets it", usesVersionSpecified)
			})

			When("version is *", func() {
				BeforeEach(func() {
					version = "*"
				})
				It("creates a bundle with the latest version it got from the index", bundleCreatedWithLatestVersion)
				It("uses the version specified if later the user sets it", usesVersionSpecified)
			})
		})

		When("connecting to a https server", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-https", targets)

				// version is empty
				helmapp.Spec.Helm.Version = ""
				// reset secret, no auth is required
				helmapp.Spec.HelmSecretName = ""

				svr := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, helmRepoIndex)
				}))
				DeferCleanup(func() {
					svr.Close()
				})

				// set the url to the httptest server
				helmapp.Spec.Helm.Repo = svr.URL
				helmapp.Spec.Helm.Chart = "alpine"
				helmapp.Spec.InsecureSkipTLSverify = false
			})

			It("does not create a bundle and returns and sets an error due to self signed certificate", func() {
				Consistently(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(BeNil())
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), err)
				}, 5*time.Second, time.Second).Should(Succeed())

				Eventually(func(g Gomega) {
					fh := &fleet.HelmApp{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, fh)
					g.Expect(err).ToNot(HaveOccurred())
					// check that the condition has the error
					checkConditionContains(
						g,
						fh,
						fleet.HelmAppAcceptedCondition,
						corev1.ConditionFalse,
						"tls: failed to verify certificate: x509: certificate signed by unknown authority",
					)

				}).Should(Succeed())
			})
		})

		When("connecting to a https server with insecureTLSVerify set", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-insecure", targets)

				// version is empty
				helmapp.Spec.Helm.Version = ""
				// reset secret, no auth is required
				helmapp.Spec.HelmSecretName = ""

				svr := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, helmRepoIndex)
				}))
				DeferCleanup(func() {
					svr.Close()
				})

				// set the url to the httptest server
				helmapp.Spec.Helm.Repo = svr.URL
				helmapp.Spec.Helm.Chart = "alpine"
				helmapp.Spec.InsecureSkipTLSverify = true
			})

			It("creates a bundle with the latest version it got from the index", func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					// the original helmapp has no version defined.
					// it should download version 0.2.0 as it is the
					// latest in the test helm index.html
					// set it here so the check passes and confirms
					// the version obtained was 0.2.0
					helmapp.Spec.Helm.Version = "0.2.0"
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())
			})
		})

		When("connecting to a https server with no credentials", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-nocreds", targets)

				// version is empty
				helmapp.Spec.Helm.Version = ""
				// reset secret, no auth is required
				helmapp.Spec.HelmSecretName = ""

				svr := newTLSServerWithAuth()
				DeferCleanup(func() {
					svr.Close()
				})

				// set the url to the httptest server
				helmapp.Spec.Helm.Repo = svr.URL
				helmapp.Spec.Helm.Chart = "alpine"
				helmapp.Spec.InsecureSkipTLSverify = true
			})

			It("does not create a bundle and returns and sets an error due to bad auth", func() {
				Consistently(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(BeNil())
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), err)
				}, 5*time.Second, time.Second).Should(Succeed())

				Eventually(func(g Gomega) {
					fh := &fleet.HelmApp{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, fh)
					g.Expect(err).ToNot(HaveOccurred())
					// check that the condition has the error
					checkConditionContains(
						g,
						fh,
						fleet.HelmAppAcceptedCondition,
						corev1.ConditionFalse,
						"error code: 401, response body: Unauthorized",
					)

				}).Should(Succeed())
			})
		})

		When("connecting to a https server with wrong credentials in a secret", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-wrongcreds", targets)

				// version is empty
				helmapp.Spec.Helm.Version = ""
				// reset secret, no auth is required
				helmapp.Spec.HelmSecretName = ""

				svr := newTLSServerWithAuth()
				DeferCleanup(func() {
					svr.Close()
				})

				// set the url to the httptest server
				helmapp.Spec.Helm.Repo = svr.URL
				helmapp.Spec.Helm.Chart = "alpine"
				helmapp.Spec.InsecureSkipTLSverify = true

				// create secret with credentials
				secretName := "supermegasecret"
				doAfterNamespaceCreated = func() {
					secret := &v1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      secretName,
							Namespace: helmapp.Namespace,
						},
						Data: map[string][]byte{v1.BasicAuthUsernameKey: []byte(authUsername), v1.BasicAuthPasswordKey: []byte("badPassword")},
						Type: v1.SecretTypeBasicAuth,
					}
					err := k8sClient.Create(ctx, secret)
					Expect(err).ToNot(HaveOccurred())
				}

				helmapp.Spec.HelmSecretName = secretName
			})

			It("does not create a bundle and returns and sets an error due to bad auth", func() {
				Consistently(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(BeNil())
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), err)
				}, 5*time.Second, time.Second).Should(Succeed())

				Eventually(func(g Gomega) {
					fh := &fleet.HelmApp{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, fh)
					g.Expect(err).ToNot(HaveOccurred())
					// check that the condition has the error
					checkConditionContains(
						g,
						fh,
						fleet.HelmAppAcceptedCondition,
						corev1.ConditionFalse,
						"error code: 401, response body: Unauthorized",
					)

				}).Should(Succeed())
			})
		})

		When("connecting to a https server with correct credentials in a secret", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-creds", targets)

				// version is empty
				helmapp.Spec.Helm.Version = ""
				// reset secret, no auth is required
				helmapp.Spec.HelmSecretName = ""

				svr := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, helmRepoIndex)
				}))
				DeferCleanup(func() {
					svr.Close()
				})

				// set the url to the httptest server
				helmapp.Spec.Helm.Repo = svr.URL
				helmapp.Spec.Helm.Chart = "alpine"
				helmapp.Spec.InsecureSkipTLSverify = true

				// create secret with credentials
				secretName := "supermegasecret"
				doAfterNamespaceCreated = func() {
					secret := &v1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      secretName,
							Namespace: helmapp.Namespace,
						},
						Data: map[string][]byte{v1.BasicAuthUsernameKey: []byte(authUsername), v1.BasicAuthPasswordKey: []byte(authPassword)},
						Type: v1.SecretTypeBasicAuth,
					}
					err := k8sClient.Create(ctx, secret)
					Expect(err).ToNot(HaveOccurred())
				}

				helmapp.Spec.HelmSecretName = secretName
			})

			It("creates a bundle with the latest version it got from the index", func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					// the original helmapp has no version defined.
					// it should download version 0.2.0 as it is the
					// latest in the test helm index.html
					// set it here so the check passes and confirms
					// the version obtained was 0.2.0
					helmapp.Spec.Helm.Version = "0.2.0"
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())
			})
		})

		When("connecting to a https server with correct credentials in a secret and caBundle", func() {
			BeforeEach(func() {
				targets = []fleet.BundleTarget{}
				helmapp = getRandomHelmAppWithTargets("test-cabundle", targets)

				// version is empty
				helmapp.Spec.Helm.Version = ""
				// reset secret, no auth is required
				helmapp.Spec.HelmSecretName = ""

				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, helmRepoIndex)
				})

				svr, err := getNewCustomTLSServer(handler)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					svr.Close()
				})

				// set the url to the httptest server
				helmapp.Spec.Helm.Repo = svr.URL
				helmapp.Spec.Helm.Chart = "alpine"

				// create secret with credentials
				secretName := "supermegasecret"
				rootCert, err := os.ReadFile("assets/root.crt")
				Expect(err).ToNot(HaveOccurred())
				doAfterNamespaceCreated = func() {
					secret := &v1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      secretName,
							Namespace: helmapp.Namespace,
						},
						Data: map[string][]byte{
							v1.BasicAuthUsernameKey: []byte(authUsername),
							v1.BasicAuthPasswordKey: []byte(authPassword),
							// use the certificate from the httptest server
							"cacerts": rootCert,
						},
						Type: v1.SecretTypeBasicAuth,
					}
					err := k8sClient.Create(ctx, secret)
					Expect(err).ToNot(HaveOccurred())
				}

				helmapp.Spec.HelmSecretName = secretName
				helmapp.Spec.InsecureSkipTLSverify = false
			})

			It("creates a bundle with the latest version it got from the index", func() {
				Eventually(func(g Gomega) {
					bundle := &fleet.Bundle{}
					ns := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
					err := k8sClient.Get(ctx, ns, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					t := []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					}
					// the original helmapp has no version defined.
					// it should download version 0.2.0 as it is the
					// latest in the test helm index.html
					// set it here so the check passes and confirms
					// the version obtained was 0.2.0
					helmapp.Spec.Helm.Version = "0.2.0"
					checkBundleIsAsExpected(g, *bundle, helmapp, t)
				}).Should(Succeed())
			})
		})
	})
})
