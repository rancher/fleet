package singlecluster_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/infra/cmd"
	"github.com/rancher/fleet/e2e/testenv/k8sclient"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func createOCIRegistrySecret(
	secretName,
	reference string,
	insecure,
	useReaderAsWriter,
	useNoDeleterUser bool) {
	namespace := env.Namespace
	username := os.Getenv("CI_OCI_USERNAME")
	password := os.Getenv("CI_OCI_PASSWORD")
	agentUsername := os.Getenv("CI_OCI_READER_USERNAME")
	agentPassword := os.Getenv("CI_OCI_READER_PASSWORD")

	if useReaderAsWriter {
		username = agentUsername
		password = agentPassword
	} else if useNoDeleterUser {
		username = os.Getenv("CI_OCI_NO_DELETER_USERNAME")
		password = os.Getenv("CI_OCI_NO_DELETER_PASSWORD")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			ocistorage.OCISecretReference:     []byte(reference),
			ocistorage.OCISecretUsername:      []byte(username),
			ocistorage.OCISecretPassword:      []byte(password),
			ocistorage.OCISecretAgentUsername: []byte(agentUsername),
			ocistorage.OCISecretAgentPassword: []byte(agentPassword),
			ocistorage.OCISecretInsecure:      []byte(strconv.FormatBool(insecure)),
			ocistorage.OCISecretBasicHTTP:     []byte(strconv.FormatBool(false)),
		},
		Type: corev1.SecretType(fleet.SecretTypeOCIStorage),
	}
	k8sclient.CreateObjectShouldSucceed(clientUpstream, secret)
}

func createDefaultOCIRegistrySecret(
	reference string,
	insecure,
	useReaderAsWriter,
	useNoDeleterUser bool) {
	createOCIRegistrySecret(
		config.DefaultOCIStorageSecretName,
		reference,
		insecure,
		useReaderAsWriter,
		useNoDeleterUser,
	)
}

// getActualEnvVariable returns the value of the given env variable
// in the gitjob and fleet-controller deployments.
// In order to be considered correct, both values should match
func getActualEnvVariable(k kubectl.Command, env string) string {
	actualValueController, err := checkEnvVariable(k, "deployment/fleet-controller", env)
	Expect(err).ToNot(HaveOccurred())
	actualValueGitjob, err := checkEnvVariable(k, "deployment/gitjob", env)
	Expect(err).ToNot(HaveOccurred())
	// both values should be the same, otherwise is a clear symptom that something went wrong
	Expect(actualValueController).To(Equal(actualValueGitjob))
	GinkgoWriter.Printf("Actual env variable values: CONTROLLER: %q, GITJOB: %q\n", actualValueController, actualValueGitjob)
	return actualValueController
}

// checkEnvVariable runs a kubectl set env --list command for the given component
// and returns the value of the given env variable.
// The value returned is a string representing the boolean value or "unset" if the env
// variable is not set.
func checkEnvVariable(k kubectl.Command, component string, env string) (string, error) {
	ns := "cattle-fleet-system"
	out, err := k.Namespace(ns).Run("set", "env", component, "--list")
	Expect(err).ToNot(HaveOccurred())
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, env) {
			keyValue := strings.Split(line, "=")
			Expect(keyValue).To(HaveLen(2))
			boolVal, err := strconv.ParseBool(keyValue[1])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%t", boolVal), nil
		}
	}
	return "unset", nil
}

// updateOCIStorageFlagValue updates the oci storage env variable to the given value.
// It compares the actual value vs the one given and, if they are different, updates the
// fleet-controller and gitjob deployments to set the given one.
// It waits until the pods related to both deployments are restarted.
func updateOCIStorageFlagValue(k kubectl.Command, value string) {
	actualEnvVal := getActualEnvVariable(k, ocistorage.OCIStorageFlag)
	GinkgoWriter.Printf("Env variable value to be used in this test: %q\n", value)
	if actualEnvVal == value {
		return
	}
	ns := "cattle-fleet-system"
	// get the actual fleet-controller and gitjob pods
	// When we change the environments variables of the deployment the pods will restart
	var controllerPod string
	var err error
	Eventually(func() string {
		controllerPod, err = k.Namespace(ns).Get("pod", "-l", "app=fleet-controller", "-l", "fleet.cattle.io/shard-default=true", "-o", "name")
		Expect(err).ToNot(HaveOccurred())
		return controllerPod
	}).WithTimeout(time.Second * 60).Should(ContainSubstring("pod/fleet-controller-"))

	var gitjobPod string
	Eventually(func() string {
		gitjobPod, err = k.Namespace(ns).Get("pod", "-l", "app=gitjob", "-o", "name")
		Expect(err).ToNot(HaveOccurred())
		return gitjobPod
	}).WithTimeout(time.Second * 60).Should(ContainSubstring("pod/gitjob-"))

	// set the env value to the deployments
	strEnvValue := ""
	if value == "unset" {
		// if the value is unset it adds a '-' at the end so kubectl deletes the env variable.
		strEnvValue = fmt.Sprintf("%s-", ocistorage.OCIStorageFlag)
	} else {
		strEnvValue = fmt.Sprintf("%s=%s", ocistorage.OCIStorageFlag, value)
	}
	_, _ = k.Namespace(ns).Run("set", "env", "deployment/fleet-controller", strEnvValue)
	_, _ = k.Namespace(ns).Run("set", "env", "deployment/gitjob", strEnvValue)

	// wait for both pods to restart with the right value for the feature env var
	Eventually(func(g Gomega) {
		g.Expect(err).ToNot(HaveOccurred())

		labelSelector := client.MatchingLabels{
			"app":                           "fleet-controller",
			"fleet.cattle.io/shard-default": "true",
		}
		valueNow, controllerPodNow, err := k8sclient.GetPodEnvVariable(clientUpstream, ocistorage.OCIStorageFlag, labelSelector)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(controllerPodNow).ToNot(BeEmpty())
		g.Expect(controllerPodNow).To(ContainSubstring("fleet-controller-"))
		g.Expect(controllerPodNow).NotTo(ContainSubstring(controllerPod))

		g.Expect(valueNow).To(Equal(value))
	}).WithTimeout(time.Second * 30).Should(Succeed())

	Eventually(func(g Gomega) {
		labelSelector := client.MatchingLabels{
			"app":                           "gitjob",
			"fleet.cattle.io/shard-default": "true",
		}

		valueNow, gitjobPodNow, err := k8sclient.GetPodEnvVariable(clientUpstream, ocistorage.OCIStorageFlag, labelSelector)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(gitjobPodNow).ToNot(BeEmpty())
		g.Expect(gitjobPodNow).To(ContainSubstring("gitjob-"))
		g.Expect(gitjobPodNow).NotTo(ContainSubstring(gitjobPod))

		g.Expect(valueNow).To(Equal(value))
	}).WithTimeout(time.Second * 30).Should(Succeed())
}

func pushFakeOCIContents(secret, manifestID string) {
	fakeManifest := &manifest.Manifest{
		Commit: "some-bad-commit",
		Resources: []fleet.BundleResource{
			{
				Name:    "test-resource",
				Content: "some-test-content",
			},
		},
	}
	secretKey := client.ObjectKey{Name: secret, Namespace: env.Namespace}
	opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
	Expect(err).ToNot(HaveOccurred())
	err = ocistorage.NewOCIWrapper().PushManifest(context.TODO(), opts, manifestID, fakeManifest)
	Expect(err).ToNot(HaveOccurred())
}

func verifyClonedSecret(originalName, originalNamespace, name, namespace string) {
	var originalSecret, secret corev1.Secret
	k8sclient.GetObjectShouldSucceed(clientUpstream, originalName, originalNamespace, &originalSecret)
	k8sclient.GetObjectShouldSucceed(clientUpstream, name, namespace, &secret)

	Expect(originalSecret.Data).To(Equal(secret.Data))
	Expect(originalSecret.Type).To(Equal(secret.Type))

	v, ok := secret.Labels[fleet.InternalSecretLabel]
	Expect(ok).To(BeTrue())
	Expect(v).To(Equal("true"))
}

func getOCIRegistryExternalIP(k kubectl.Command) string {
	if v := os.Getenv("external_ip"); v != "" {
		return v
	}

	externalIP, err := k.Namespace(cmd.InfraNamespace).Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
	Expect(err).ToNot(HaveOccurred(), externalIP)
	return externalIP
}

var _ = Describe("Single Cluster Deployments using OCI registry", Label("oci-registry", "infra-setup"), func() {
	const asset = "single-cluster/test-oci.yaml"
	var (
		k                   kubectl.Command
		tempDir             string
		contentsID          string
		downstreamNamespace string
		defaultOCIRegistry  string
		ociRegistry         string
		envVarValueBefore   string
		contentIDToPurge    string

		// user-defined variables for defining the test behaviour
		envVarValue               string
		insecureSkipTLS           bool
		deployDefaultSecret       bool
		deploySpecificSecretName  string
		forceDefaultReference     string
		forceUserDefinedReference string
		useReaderAsWriter         bool
		useNoDeleterUser          bool
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		tempDir = GinkgoT().TempDir()
		externalIP := getOCIRegistryExternalIP(k)
		Expect(net.ParseIP(externalIP)).ShouldNot(BeNil())
		defaultOCIRegistry = fmt.Sprintf("%s:8082", externalIP)
		ociRegistry = defaultOCIRegistry

		// store the actual value of the env value
		// we'll restore in the AfterEach statement if needed
		envVarValueBefore = getActualEnvVariable(k, ocistorage.OCIStorageFlag)

		// reset the value of the contents resource to purge
		contentIDToPurge = ""
	})

	JustBeforeEach(func() {
		contentsID = ""
		contentIDToPurge = ""

		updateOCIStorageFlagValue(k, envVarValue)

		Expect(os.Getenv("CI_OCI_USERNAME")).NotTo(BeEmpty())
		Expect(os.Getenv("CI_OCI_PASSWORD")).NotTo(BeEmpty())
		Expect(os.Getenv("CI_OCI_READER_USERNAME")).NotTo(BeEmpty())
		Expect(os.Getenv("CI_OCI_READER_PASSWORD")).NotTo(BeEmpty())

		gitrepo := path.Join(tempDir, "gitrepo.yaml")

		if forceDefaultReference != "" {
			defaultOCIRegistry = forceDefaultReference
		}
		if forceUserDefinedReference != "" {
			ociRegistry = forceUserDefinedReference
		}

		if deployDefaultSecret {
			createDefaultOCIRegistrySecret(
				defaultOCIRegistry,
				insecureSkipTLS,
				useReaderAsWriter,
				useNoDeleterUser)
		}

		if deploySpecificSecretName != "" {
			createOCIRegistrySecret(
				deploySpecificSecretName,
				ociRegistry,
				insecureSkipTLS,
				useReaderAsWriter,
				useNoDeleterUser)
		}

		err := testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
			OCIRegistrySecret string
			Branch            string
		}{
			deploySpecificSecretName,
			"master",
		})
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)

		var cluster fleet.Cluster
		k8sclient.GetObjectShouldSucceed(clientUpstream, "local", env.Namespace, &cluster)
		downstreamNamespace = cluster.Status.Namespace
	})

	AfterEach(func() {
		var ociOpts ocistorage.OCIOpts
		var err error
		// We retrieve the OCI secret now in order to access the OCI registry later.
		if contentsID != "" && contentIDToPurge == "" {
			secretKey := client.ObjectKey{Name: contentsID, Namespace: env.Namespace}
			ociOpts, err = ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
			Expect(err).ToNot(HaveOccurred())
		}

		if deployDefaultSecret {
			var secret corev1.Secret
			k8sclient.GetObjectShouldSucceed(clientUpstream, config.DefaultOCIStorageSecretName, env.Namespace, &secret)
			k8sclient.DeleteObjectShouldSucceed(clientUpstream, &secret)
		}
		if deploySpecificSecretName != "" {
			var secret corev1.Secret
			k8sclient.GetObjectShouldSucceed(clientUpstream, deploySpecificSecretName, env.Namespace, &secret)
			k8sclient.DeleteObjectShouldSucceed(clientUpstream, &secret)
		}

		gitrepo := path.Join(tempDir, "gitrepo.yaml")
		out, err := k.Delete("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)

		// secrets for bundle and bundledeployment should be gone
		// secret always begin with "s-"
		Eventually(func() string {
			out, _ := k.Namespace("fleet-local").Get("secrets", "-o", "name")
			return out
		}).ShouldNot(ContainSubstring("secret/s-"))

		Eventually(func() string {
			out, _ := k.Namespace(downstreamNamespace).Get("secrets", "-o", "name")
			return out
		}).ShouldNot(ContainSubstring("secret/s-"))

		// reset back env var value if needed
		if envVarValue != envVarValueBefore {
			GinkgoWriter.Printf("Restoring env variable back to %q\n", envVarValueBefore)
			updateOCIStorageFlagValue(k, envVarValueBefore)
		}

		// check that contents have been purged when deleting the gitrepo
		if contentIDToPurge != "" {
			k8sclient.ObjectShouldNotExist(clientUpstream, contentIDToPurge, "", &fleet.Content{}, true)
		} else if contentsID != "" {
			// check that the oci artifact was deleted
			_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), ociOpts, contentsID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		}

		_, err = k.Delete("events", "-n", env.Namespace, "--all")
		Expect(err).ToNot(HaveOccurred())
	})

	ReportAfterEach(func(report SpecReport) {
		GinkgoWriter.Printf("____________________________________________________________________")
		if report.Failed() {
			GinkgoWriter.Println("🔍 Spec failed (including AfterEach). Gathering logs...")

			getPodLogs("fleet-controller")
			getPodLogs("gitjob")
		}
	})

	When("applying a gitrepo with the default ociSecret also deployed", func() {
		Context("containing a valid helm chart", func() {
			BeforeEach(func() {
				envVarValue = "unset"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = false
			})

			It("deploys the bundle", func() {
				var bundle fleet.Bundle
				By("creating the bundle", func() {
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					Expect(bundle.Spec.ContentsID).To(ContainSubstring("s-"))
					contentsID = bundle.Spec.ContentsID
				})

				By("setting the OCI reference status field key to the OCI path of the manifest", func() {
					Eventually(func(g Gomega) {
						var bundle fleet.Bundle
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
						g.Expect(bundle.Status.OCIReference).To(Equal(fmt.Sprintf("oci://%s/%s:latest", ociRegistry, contentsID)))
					}).Should(Succeed())
				})
				By("creating a bundle secret with the expected contents", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, contentsID, env.Namespace)
				})
				By("creating a bundledeployment secret", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, contentsID, downstreamNamespace)
				})
				By("not creating a contents resource for this chart", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, "", &fleet.Content{}, false)
				})
				By("deploying the helm chart", func() {
					var cm corev1.ConfigMap
					k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
				})
				By("creating an OCI artifact with the expected name", func() {
					secretKey := client.ObjectKey{Name: contentsID, Namespace: env.Namespace}
					opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
					Expect(err).ToNot(HaveOccurred())
					_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), opts, contentsID)
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})
	})

	When("applying a gitrepo with the default ociSecret with wrong reference and the user-defined secret also deployed with correct values", func() {
		Context("containing a valid helm chart", func() {
			BeforeEach(func() {
				envVarValue = "true"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = "test-secret"
				forceDefaultReference = "not-valid-oci-registry.com"
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = false
			})

			It("deploys the bundle", func() {
				var bundle fleet.Bundle
				By("creating the bundle", func() {
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					Expect(bundle.Spec.ContentsID).To(ContainSubstring("s-"))
					contentsID = bundle.Spec.ContentsID
				})

				By("setting the OCI reference status field key to the OCI path of the manifest", func() {
					Eventually(func(g Gomega) {
						var bundle fleet.Bundle
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
						g.Expect(bundle.Status.OCIReference).To(Equal(fmt.Sprintf("oci://%s/%s:latest", ociRegistry, contentsID)))
					}).Should(Succeed())
				})
				By("creating a bundle secret with the expected contents from the user-defined secret", func() {
					verifyClonedSecret(deploySpecificSecretName, env.Namespace, contentsID, env.Namespace)
				})
				By("creating a bundledeployment secret", func() {
					verifyClonedSecret(deploySpecificSecretName, env.Namespace, contentsID, downstreamNamespace)
				})
				By("not creating a contents resource for this chart", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, "", &fleet.Content{}, false)
				})
				By("deploying the helm chart", func() {
					var cm corev1.ConfigMap
					k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
				})
			})
		})
	})

	When("applying a gitrepo with the default ociSecret also deployed with incorrect reference and OCI env var is not set", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				envVarValue = "false"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = "not-valid-oci-registry.com"
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = false
			})

			It("creates the bundle with no OCI storage", func() {
				var bundle fleet.Bundle
				By("creating the bundle", func() {
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("not setting the ContentsID field in the bundle", func() {
					Expect(bundle.Spec.ContentsID).To(BeEmpty())
				})
				By("creating a bundle deployment", func() {
					var bd fleet.BundleDeployment
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", downstreamNamespace, &bd)
					Expect(bd.Spec.DeploymentID).ToNot(BeEmpty())
					tokens := strings.Split(bd.Spec.DeploymentID, ":")
					Expect(tokens).To(HaveLen(2))
					contentsID = tokens[0]
					// save the contents id to purge after the test
					// this will prevent interference with the previous test
					// as the resources sha256 is the same
					contentIDToPurge = contentsID
				})
				By("creating a content resource with the expected ID", func() {
					var content fleet.Content
					k8sclient.GetObjectShouldSucceed(clientUpstream, contentsID, "", &content)
				})
				By("not creating a bundle secret", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, env.Namespace, &corev1.Secret{}, false)
				})
				By("not creating a bundle deployment secret", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, downstreamNamespace, &corev1.Secret{}, false)
				})
				By("deploying the helm chart", func() {
					var cm corev1.ConfigMap
					k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
				})
			})
		})
	})

	When("creating a gitrepo resource with invalid ociRegistry info", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				envVarValue = "true"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = "not-valid-oci-registry.com"
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = false
			})

			It("does not create the bundle", func() {
				By("setting the right error message in the GitRepo", func() {
					Eventually(func(g Gomega) {
						var repo fleet.GitRepo
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample", env.Namespace, &repo)
						stalledFound := false
						stalledMessage := ""
						for _, cond := range repo.Status.Conditions {
							if cond.Type == "Stalled" {
								stalledMessage = cond.Message
								stalledFound = true
								break
							}
						}
						g.Expect(stalledFound).To(BeTrue())
						g.Expect(stalledMessage).To(ContainSubstring("no such host"))
					}).Should(Succeed())
				})
				By("not creating the bundle", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, "sample-simple-chart-oci", env.Namespace, &fleet.Bundle{}, true)
				})
				By("not creating the bundle secret", func() {
					var secrets corev1.SecretList
					err := clientUpstream.List(context.TODO(), &secrets, &client.ListOptions{Namespace: env.Namespace})
					Expect(err).ToNot(HaveOccurred())
					for _, s := range secrets.Items {
						Expect(s.Name).ToNot(HavePrefix("s-"))
					}
				})
			})
		})
	})

	When("creating a gitrepo resource with valid ociRegistry but not ignoring certs", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				envVarValue = "true"
				insecureSkipTLS = false
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = false
			})

			It("does not create the bundle", func() {
				By("setting the right error message in the GitRepo", func() {
					Eventually(func(g Gomega) {
						var repo fleet.GitRepo
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample", env.Namespace, &repo)
						stalledFound := false
						stalledMessage := ""
						for _, cond := range repo.Status.Conditions {
							if cond.Type == "Stalled" {
								stalledMessage = cond.Message
								stalledFound = true
								break
							}
						}
						g.Expect(stalledFound).To(BeTrue())
						g.Expect(stalledMessage).To(ContainSubstring("tls: failed to verify certificate: x509"))
					}).Should(Succeed())
				})
				By("not creating the bundle", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, "sample-simple-chart-oci", env.Namespace, &fleet.Bundle{}, true)
				})
				By("not creating the bundle secret", func() {
					var secrets corev1.SecretList
					err := clientUpstream.List(context.TODO(), &secrets, &client.ListOptions{Namespace: env.Namespace})
					Expect(err).ToNot(HaveOccurred())
					for _, s := range secrets.Items {
						Expect(s.Name).ToNot(HavePrefix("s-"))
					}
				})
			})
		})
	})

	When("applying a gitrepo and the OCI artifact is changed with invalid contents", func() {
		Context("containing a valid helm chart", func() {
			BeforeEach(func() {
				envVarValue = "true"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = false
			})

			It("deploys the bundle and rejects the content as it's different from the expected", func() {
				var bundle fleet.Bundle
				By("creating the bundle", func() {
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					Expect(bundle.Spec.ContentsID).To(ContainSubstring("s-"))
					contentsID = bundle.Spec.ContentsID
					Expect(bundle.Spec.ValuesHash).To(BeEmpty())
				})

				By("setting the OCI reference status field key to the OCI path of the manifest", func() {
					Eventually(func(g Gomega) {
						var bundle fleet.Bundle
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
						g.Expect(bundle.Status.OCIReference).To(Equal(fmt.Sprintf("oci://%s/%s:latest", ociRegistry, contentsID)))
					}).Should(Succeed())
				})
				By("creating a bundle secret with the expected contents", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, contentsID, env.Namespace)
				})
				By("creating a bundledeployment secret", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, contentsID, downstreamNamespace)
				})
				By("not creating a contents resource for this chart", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, "", &fleet.Content{}, false)
				})
				By("deploying the helm chart", func() {
					var cm corev1.ConfigMap
					k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
				})
				By("changing the contents of the oci artifact", func() {
					pushFakeOCIContents(config.DefaultOCIStorageSecretName, contentsID)
				})
				By("forcing the bundle deployment to re-deploy and reject the oci contents", func() {
					var bd fleet.BundleDeployment
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", downstreamNamespace, &bd)
					// force the bundledeployment to re-deploy by deleting it
					k8sclient.DeleteObjectShouldSucceed(clientUpstream, &bd)
					Eventually(func(g Gomega) {
						var repo fleet.GitRepo
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample", env.Namespace, &repo)
						conditionFound := false
						message := ""
						for _, cond := range repo.Status.Conditions {
							if cond.Type == "Ready" {
								message = cond.Message
								conditionFound = true
								break
							}
						}
						g.Expect(conditionFound).To(BeTrue())
						g.Expect(message).To(ContainSubstring("invalid or corrupt manifest"))
					}).Should(Succeed())
				})
			})
		})
	})

	When("creating a gitrepo resource using an oci registry secret with a username that has no write permissions", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				envVarValue = "true"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
				useReaderAsWriter = true
				useNoDeleterUser = false
			})

			It("does not create the bundle", func() {
				By("setting the right error message in the GitRepo", func() {
					Eventually(func(g Gomega) {
						var repo fleet.GitRepo
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample", env.Namespace, &repo)
						stalledFound := false
						stalledMessage := ""
						for _, cond := range repo.Status.Conditions {
							if cond.Type == "Stalled" {
								stalledMessage = cond.Message
								stalledFound = true
								break
							}
						}
						g.Expect(stalledFound).To(BeTrue())
						g.Expect(stalledMessage).To(ContainSubstring("requested access to the resource is denied"))
					}).Should(Succeed())
				})
				By("not creating the bundle", func() {
					k8sclient.ObjectShouldNotExist(clientUpstream, "sample-simple-chart-oci", env.Namespace, &fleet.Bundle{}, true)
				})
				By("not creating the bundle secret", func() {
					var secrets corev1.SecretList
					err := clientUpstream.List(context.TODO(), &secrets, &client.ListOptions{Namespace: env.Namespace})
					Expect(err).ToNot(HaveOccurred())
					for _, s := range secrets.Items {
						Expect(s.Name).ToNot(HavePrefix("s-"))
					}
				})
			})
		})
	})

	When("applying a gitrepo and then applying a new one with different contents and the same name", func() {
		Context("containing a valid helm chart and later some raw contents", func() {
			BeforeEach(func() {
				envVarValue = "true"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = false
			})

			It("redeploys the gitrepo and only the last OCI artifact and related secret should remain", func() {
				var bundle fleet.Bundle
				By("creating the bundle", func() {
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					Expect(bundle.Spec.ContentsID).To(ContainSubstring("s-"))
					contentsID = bundle.Spec.ContentsID
				})
				By("waiting until the helm chart is deployed", func() {
					Eventually(func(g Gomega) {
						var cm corev1.ConfigMap
						k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
					}).Should(Succeed())
				})
				By("creating an OCI artifact with the expected name", func() {
					secretKey := client.ObjectKey{Name: contentsID, Namespace: env.Namespace}
					opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
					Expect(err).ToNot(HaveOccurred())
					_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), opts, contentsID)
					Expect(err).ToNot(HaveOccurred())
				})
				By("deploying a gitrepo with the same name and different content", func() {
					gitrepo := path.Join(tempDir, "gitrepo.yaml")
					err := testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
						OCIRegistrySecret string
						Branch            string
					}{
						deploySpecificSecretName,
						"oci-storage-git-repo-changed",
					})
					Expect(err).ToNot(HaveOccurred())

					out, err := k.Apply("-f", gitrepo)
					Expect(err).ToNot(HaveOccurred(), out)
				})
				var bundleNow fleet.Bundle
				previousContentsID := contentsID
				By("updating the bundle, setting ContentsID to a different value", func() {
					Eventually(func(g Gomega) {
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundleNow)
						g.Expect(bundleNow.Spec.ContentsID).To(ContainSubstring("s-"))
						g.Expect(previousContentsID).ToNot(Equal((bundleNow.Spec.ContentsID)))
						contentsID = bundleNow.Spec.ContentsID
					}).Should(Succeed())
				})
				By("checking that the previous oci bundle key was deleted", func() {
					Eventually(func(g Gomega) {
						err := clientUpstream.Get(context.TODO(), client.ObjectKey{Name: previousContentsID, Namespace: env.Namespace}, &corev1.Secret{})
						g.Expect(errors.IsNotFound(err)).To(BeTrue())
					}).Should(Succeed())
				})
				By("checking that the previous oci bundle deployment key was deleted", func() {
					Eventually(func(g Gomega) {
						err := clientUpstream.Get(context.TODO(), client.ObjectKey{Name: previousContentsID, Namespace: downstreamNamespace}, &corev1.Secret{})
						g.Expect(errors.IsNotFound(err)).To(BeTrue())
					}).Should(Succeed())
				})
				By("checking that the previous oci artifact was deleted", func() {
					// use the secret for the new contentID because it has the same contents.
					secretKey := client.ObjectKey{Name: contentsID, Namespace: env.Namespace}
					opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
					Expect(err).ToNot(HaveOccurred())
					_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), opts, previousContentsID)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("not found"))
				})
				By("creating a bundle secret with the expected contents", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, contentsID, env.Namespace)
				})
				By("creating a bundledeployment secret", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, contentsID, downstreamNamespace)
				})
				By("creating an OCI artifact with the expected name", func() {
					secretKey := client.ObjectKey{Name: contentsID, Namespace: env.Namespace}
					opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
					Expect(err).ToNot(HaveOccurred())
					_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), opts, contentsID)
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})
	})

	When("applying a gitrepo and then applying a new one with different contents and the same name", func() {
		Context("containing a valid helm chart and later some raw contents and using a user that has no delete permissions", func() {
			BeforeEach(func() {
				envVarValue = "true"
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
				useReaderAsWriter = false
				useNoDeleterUser = true
			})

			It("redeploys the gitrepo and and keeps the previous oci artifact", func() {
				var bundle fleet.Bundle
				By("creating the bundle", func() {
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					Expect(bundle.Spec.ContentsID).To(ContainSubstring("s-"))
				})
				By("waiting until the helm chart is deployed", func() {
					Eventually(func(g Gomega) {
						var cm corev1.ConfigMap
						k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
					}).Should(Succeed())
				})

				previousContentsID := bundle.Spec.ContentsID

				By("creating an OCI artifact with the expected name", func() {
					secretKey := client.ObjectKey{Name: bundle.Spec.ContentsID, Namespace: env.Namespace}
					opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
					Expect(err).ToNot(HaveOccurred())
					_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), opts, bundle.Spec.ContentsID)
					Expect(err).ToNot(HaveOccurred())
				})
				By("deploying a gitrepo with the same name and different content", func() {
					gitrepo := path.Join(tempDir, "gitrepo.yaml")
					err := testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
						OCIRegistrySecret string
						Branch            string
					}{
						deploySpecificSecretName,
						"oci-storage-git-repo-changed",
					})
					Expect(err).ToNot(HaveOccurred())

					out, err := k.Apply("-f", gitrepo)
					Expect(err).ToNot(HaveOccurred(), out)
				})
				var bundleNow fleet.Bundle
				By("updating the bundle, setting ContentsID to a different value", func() {
					Eventually(func(g Gomega) {
						k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundleNow)
						g.Expect(bundleNow.Spec.ContentsID).To(ContainSubstring("s-"))
						g.Expect(previousContentsID).ToNot(Equal((bundleNow.Spec.ContentsID)))
					}).Should(Succeed())
				})
				// The bundle secret is kept because the oci artifact could not be
				// deleted.
				By("checking that the previous oci bundle key was not deleted", func() {
					var secret corev1.Secret
					k8sclient.GetObjectShouldSucceed(clientUpstream, previousContentsID, env.Namespace, &secret)
				})
				// The bundle deployment secret is deleted because the oci artifact is no longer
				// going to be deployed
				By("checking that the previous oci bundle deployment key was deleted", func() {
					var secret corev1.Secret
					k8sclient.ObjectShouldNotExist(clientUpstream, previousContentsID, downstreamNamespace, &secret, false)
				})
				By("checking that the previous oci artifact was not deleted", func() {
					secretKey := client.ObjectKey{Name: previousContentsID, Namespace: env.Namespace}
					opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
					Expect(err).ToNot(HaveOccurred())
					_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), opts, previousContentsID)
					Expect(err).ToNot(HaveOccurred())
				})
				By("creating a new bundle secret with the expected contents", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, bundleNow.Spec.ContentsID, env.Namespace)
				})
				By("creating a new bundledeployment secret", func() {
					verifyClonedSecret(config.DefaultOCIStorageSecretName, env.Namespace, bundleNow.Spec.ContentsID, downstreamNamespace)
				})
				By("creating an OCI artifact with the expected name", func() {
					secretKey := client.ObjectKey{Name: bundleNow.Spec.ContentsID, Namespace: env.Namespace}
					opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
					Expect(err).ToNot(HaveOccurred())
					_, err = ocistorage.NewOCIWrapper().PullManifest(context.TODO(), opts, bundleNow.Spec.ContentsID)
					Expect(err).ToNot(HaveOccurred())
				})
				By("creating 1 event to warn the user that the previous oci artifact could not be deleted", func() {
					var events corev1.EventList
					err := clientUpstream.List(context.TODO(), &events, &client.ListOptions{Namespace: env.Namespace})
					Expect(err).ToNot(HaveOccurred())

					var ociEvents []corev1.Event
					for _, event := range events.Items {
						if strings.Contains(event.Message, previousContentsID) &&
							strings.Contains(event.Message, "deleting OCI artifact") &&
							event.Reason == "FailedToDeleteOCIArtifact" &&
							event.Source.Component == "fleet-apply" {
							ociEvents = append(ociEvents, event)
						}
					}
					Expect(ociEvents).To(HaveLen(1))
				})
			})
		})
	})
})
