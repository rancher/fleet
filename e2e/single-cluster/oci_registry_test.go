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
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func createOCIRegistrySecret(
	secretName,
	namespace,
	secretType,
	reference,
	username,
	password,
	agentUsername,
	agentPassword string,
	insecure,
	basicHTTP bool) {
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
			ocistorage.OCISecretBasicHTTP:     []byte(strconv.FormatBool(basicHTTP)),
		},
		Type: corev1.SecretType(secretType),
	}
	k8sclient.CreateObjectShouldSucceed(clientUpstream, secret)
}

func createDefaultOCIRegistrySecret(
	namespace,
	secretType,
	reference,
	username,
	password,
	agentUsername,
	agentPassword string,
	insecure,
	basicHTTP bool) {
	createOCIRegistrySecret(
		ocistorage.OCIStorageDefaultSecretName,
		namespace,
		secretType,
		reference,
		username,
		password,
		agentUsername,
		agentPassword,
		insecure,
		basicHTTP)
}

// getActualEnvVariable returns the value of the given env variable
// in the gitjob and fleet-controller deployments.
// In order to be considered correct, both values should match
func getActualEnvVariable(k kubectl.Command, env string) bool {
	actualValueController, err := checkEnvVariable(k, "deployment/fleet-controller", env)
	Expect(err).ToNot(HaveOccurred())
	actualValueGitjob, err := checkEnvVariable(k, "deployment/gitjob", env)
	Expect(err).ToNot(HaveOccurred())
	// both values should be the same, otherwise is a clear symptom that something went wrong
	Expect(actualValueController).To(Equal(actualValueGitjob))
	GinkgoWriter.Printf("Actual experimental env variable values: CONTROLLER: %t, GITJOB: %t\n", actualValueController, actualValueGitjob)
	return actualValueController
}

// checkEnvVariable runs a kubectl set env --list command for the given component
// and returns the value of the given env variable
func checkEnvVariable(k kubectl.Command, component string, env string) (bool, error) {
	ns := "cattle-fleet-system"
	out, err := k.Namespace(ns).Run("set", "env", component, "--list")
	Expect(err).ToNot(HaveOccurred())
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, env) {
			keyValue := strings.Split(line, "=")
			Expect(keyValue).To(HaveLen(2))
			return strconv.ParseBool(keyValue[1])
		}
	}
	return false, fmt.Errorf("Environment variable was not found")
}

// updateExperimentalFlagValue updates the oci storage experimental flag to the given value.
// It compares the actual value vs the one given and, if they are different, updates the
// fleet-controller and gitjob deployments to set the given one.
// It waits until the pods related to both deployments are restarted.
func updateExperimentalFlagValue(k kubectl.Command, value bool) {
	actualEnvVal := getActualEnvVariable(k, ocistorage.OCIStorageExperimentalFlag)
	GinkgoWriter.Printf("Env variable value to be used in this test: %t\n", value)
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

	// set the experimental env value to the deployments
	strEnvValue := fmt.Sprintf("%s=%t", ocistorage.OCIStorageExperimentalFlag, value)
	_, _ = k.Namespace(ns).Run("set", "env", "deployment/fleet-controller", strEnvValue)
	_, _ = k.Namespace(ns).Run("set", "env", "deployment/gitjob", strEnvValue)

	// wait for both pods to restart with the right value for the experimental feature env var
	Eventually(func(g Gomega) {
		out, err := k.Namespace(ns).Get(
			"pod",
			"-l", "app=fleet-controller,fleet.cattle.io/shard-default=true",
			"-o", fmt.Sprintf(`jsonpath={range .items[*]}{.metadata.name}{"\t"}{.spec.containers[0].env[?(@.name=="%s")].value}{end}`, ocistorage.OCIStorageExperimentalFlag),
		)
		g.Expect(err).ToNot(HaveOccurred())

		elts := strings.Split(out, "\t")
		controllerPodNow, valueNow := elts[0], elts[1]
		g.Expect(controllerPodNow).ToNot(BeEmpty())
		g.Expect(controllerPodNow).To(ContainSubstring("fleet-controller-"))
		g.Expect(controllerPodNow).NotTo(ContainSubstring(controllerPod))

		g.Expect(valueNow).To(Equal(fmt.Sprintf("%t", value)))
	}).WithTimeout(time.Second * 30).Should(Succeed())

	Eventually(func(g Gomega) {
		out, err := k.Namespace(ns).Get(
			"pod",
			"-l", "app=gitjob,fleet.cattle.io/shard-default=true",
			"-o", fmt.Sprintf(`jsonpath={range .items[*]}{.metadata.name}{"\t"}{.spec.containers[0].env[?(@.name=="%s")].value}{end}`, ocistorage.OCIStorageExperimentalFlag),
		)
		g.Expect(err).ToNot(HaveOccurred())

		elts := strings.Split(out, "\t")
		gitjobPodNow, valueNow := elts[0], elts[1]
		g.Expect(gitjobPodNow).ToNot(BeEmpty())
		g.Expect(gitjobPodNow).To(ContainSubstring("gitjob-"))
		g.Expect(gitjobPodNow).NotTo(ContainSubstring(gitjobPod))

		g.Expect(valueNow).To(Equal(fmt.Sprintf("%t", value)))
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
	secretKey := types.NamespacedName{Name: secret, Namespace: env.Namespace}
	opts, err := ocistorage.ReadOptsFromSecret(context.TODO(), clientUpstream, secretKey)
	Expect(err).ToNot(HaveOccurred())
	err = ocistorage.NewOCIWrapper().PushManifest(context.TODO(), opts, manifestID, fakeManifest)
	Expect(err).ToNot(HaveOccurred())
}

func verifySecretsAreEqual(originalName, originalNamespace, name, namespace string) {
	var originalSecret, secret corev1.Secret
	k8sclient.GetObjectShouldSucceed(clientUpstream, originalName, originalNamespace, &originalSecret)
	k8sclient.GetObjectShouldSucceed(clientUpstream, name, namespace, &secret)

	Expect(originalSecret.Data).To(Equal(secret.Data))
	Expect(originalSecret.Type).To(Equal(secret.Type))
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
	var (
		asset                  = "single-cluster/test-oci.yaml"
		k                      kubectl.Command
		tempDir                string
		contentsID             string
		downstreamNamespace    string
		defaultOCIRegistry     string
		ociRegistry            string
		experimentalFlagBefore bool
		contentIDToPurge       string

		// user-defined variables for defining the test behaviour
		experimentalValue         bool
		insecureSkipTLS           bool
		deployDefaultSecret       bool
		deploySpecificSecretName  string
		forceDefaultReference     string
		forceUserDefinedReference string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		tempDir = GinkgoT().TempDir()
		externalIP := getOCIRegistryExternalIP(k)
		Expect(net.ParseIP(externalIP)).ShouldNot(BeNil())
		defaultOCIRegistry = fmt.Sprintf("%s:8082", externalIP)
		ociRegistry = defaultOCIRegistry

		// store the actual value of the experimental env value
		// we'll restore in the AfterEach statement if needed
		experimentalFlagBefore = getActualEnvVariable(k, ocistorage.OCIStorageExperimentalFlag)

		// reset the value of the contents resource to purge
		contentIDToPurge = ""
	})

	JustBeforeEach(func() {
		updateExperimentalFlagValue(k, experimentalValue)
		Expect(os.Getenv("CI_OCI_USERNAME")).NotTo(BeEmpty())
		Expect(os.Getenv("CI_OCI_PASSWORD")).NotTo(BeEmpty())

		gitrepo := path.Join(tempDir, "gitrepo.yaml")

		if forceDefaultReference != "" {
			defaultOCIRegistry = forceDefaultReference
		}
		if forceUserDefinedReference != "" {
			ociRegistry = forceUserDefinedReference
		}

		if deployDefaultSecret {
			createDefaultOCIRegistrySecret(
				env.Namespace,
				fleet.SecretTypeOCIStorage,
				defaultOCIRegistry,
				os.Getenv("CI_OCI_USERNAME"),
				os.Getenv("CI_OCI_PASSWORD"),
				"",
				"",
				insecureSkipTLS,
				false)
		}

		if deploySpecificSecretName != "" {
			createOCIRegistrySecret(
				deploySpecificSecretName,
				env.Namespace,
				fleet.SecretTypeOCIStorage,
				ociRegistry,
				os.Getenv("CI_OCI_USERNAME"),
				os.Getenv("CI_OCI_PASSWORD"),
				"",
				"",
				insecureSkipTLS,
				false)
		}

		err := testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
			OCIRegistrySecret string
		}{
			deploySpecificSecretName,
		})
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)

		var cluster fleet.Cluster
		k8sclient.GetObjectShouldSucceed(clientUpstream, "local", env.Namespace, &cluster)
		downstreamNamespace = cluster.Status.Namespace
	})

	AfterEach(func() {
		if deployDefaultSecret {
			var secret corev1.Secret
			k8sclient.GetObjectShouldSucceed(clientUpstream, ocistorage.OCIStorageDefaultSecretName, env.Namespace, &secret)
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

		// reset back experimental flag value if needed
		if experimentalValue != experimentalFlagBefore {
			GinkgoWriter.Printf("Restoring experimental env variable back to %t n", experimentalFlagBefore)
			updateExperimentalFlagValue(k, experimentalFlagBefore)
		}

		// check that contents have been purged when deleting the gitrepo
		if contentIDToPurge != "" {
			k8sclient.ObjectShouldNotExist(clientUpstream, contentIDToPurge, "", &fleet.Content{}, true)
		}
	})

	When("applying a gitrepo with the default ociSecret also deployed", func() {
		Context("containing a valid helm chart", func() {
			BeforeEach(func() {
				experimentalValue = true
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
			})

			It("deploys the bundle", func() {
				By("creating the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
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
					verifySecretsAreEqual(ocistorage.OCIStorageDefaultSecretName, env.Namespace, contentsID, env.Namespace)
				})
				By("creating a bundledeployment secret", func() {
					verifySecretsAreEqual(ocistorage.OCIStorageDefaultSecretName, env.Namespace, contentsID, downstreamNamespace)
				})
				By("not creating a contents resource for this chart", func() {
					var content fleet.Content
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, "", &content, false)
				})
				By("deploying the helm chart", func() {
					var cm corev1.ConfigMap
					k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
				})
			})
		})
	})

	When("applying a gitrepo with the default ociSecret with wrong reference and the user-defined secret also deployed with correct values", func() {
		Context("containing a valid helm chart", func() {
			BeforeEach(func() {
				experimentalValue = true
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = "test-secret"
				forceDefaultReference = "not-valid-oci-registry.com"
				forceUserDefinedReference = ""
			})

			It("deploys the bundle", func() {
				By("creating the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
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
					verifySecretsAreEqual(deploySpecificSecretName, env.Namespace, contentsID, env.Namespace)
				})
				By("creating a bundledeployment secret", func() {
					verifySecretsAreEqual(deploySpecificSecretName, env.Namespace, contentsID, downstreamNamespace)
				})
				By("not creating a contents resource for this chart", func() {
					var content fleet.Content
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, "", &content, false)
				})
				By("deploying the helm chart", func() {
					var cm corev1.ConfigMap
					k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
				})
			})
		})
	})

	When("applying a gitrepo with the default ociSecret also deployed with incorrect reference and experimental flag is not set", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				experimentalValue = false
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = "not-valid-oci-registry.com"
				forceUserDefinedReference = ""
			})

			It("creates the bundle with no OCI storage", func() {
				By("creating the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("not setting the ContentsID field in the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
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
					var secret corev1.Secret
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, env.Namespace, &secret, false)
				})
				By("not creating a bundle deployment secret", func() {
					var secret corev1.Secret
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, downstreamNamespace, &secret, false)
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
				experimentalValue = true
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = "not-valid-oci-registry.com"
				forceUserDefinedReference = ""
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
					var bundle fleet.Bundle
					k8sclient.ObjectShouldNotExist(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle, true)
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
				experimentalValue = true
				insecureSkipTLS = false
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
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
					var bundle fleet.Bundle
					k8sclient.ObjectShouldNotExist(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle, true)
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
				experimentalValue = true
				insecureSkipTLS = true
				deployDefaultSecret = true
				deploySpecificSecretName = ""
				forceDefaultReference = ""
				forceUserDefinedReference = ""
			})

			It("deploys the bundle and rejects the content as it's different from the expected", func() {
				By("creating the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
				})
				By("setting the ContentsID field in the bundle", func() {
					var bundle fleet.Bundle
					k8sclient.GetObjectShouldSucceed(clientUpstream, "sample-simple-chart-oci", env.Namespace, &bundle)
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
					verifySecretsAreEqual(ocistorage.OCIStorageDefaultSecretName, env.Namespace, contentsID, env.Namespace)
				})
				By("creating a bundledeployment secret", func() {
					verifySecretsAreEqual(ocistorage.OCIStorageDefaultSecretName, env.Namespace, contentsID, downstreamNamespace)
				})
				By("not creating a contents resource for this chart", func() {
					var content fleet.Content
					k8sclient.ObjectShouldNotExist(clientUpstream, contentsID, "", &content, false)
				})
				By("deploying the helm chart", func() {
					var cm corev1.ConfigMap
					k8sclient.GetObjectShouldSucceed(clientUpstream, "test-simple-chart-config", "default", &cm)
				})
				By("changing the contents of the oci artifact", func() {
					pushFakeOCIContents(ocistorage.OCIStorageDefaultSecretName, contentsID)
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
})
