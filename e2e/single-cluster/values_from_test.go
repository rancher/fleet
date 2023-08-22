package singlecluster_test

import (
	"encoding/json"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("ValuesFrom", func() {
	var (
		// k is the kubectl command for the cluster registration namespace
		k kubectl.Command
		// kw is the kubectl command for namespace the workload is deployed to
		kw        kubectl.Command
		namespace string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		namespace = testenv.NewNamespaceName("values-from", rand.New(rand.NewSource(GinkgoRandomSeed())))
		kw = k.Namespace(namespace)

		out, err := k.Create("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Namespace(namespace).Create("secret", "generic", "secret-values",
			"--from-file=values.yaml="+testenv.AssetPath("single-cluster/values-from-secret.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Namespace("default").Create("configmap", "configmap-values",
			"--from-file=values.yaml="+testenv.AssetPath("single-cluster/values-from-configmap.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.CreateGitRepo(k, namespace, "values-from", "master", "helm-values-from")
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			out, err := k.Delete("gitrepo", "values-from")
			Expect(err).ToNot(HaveOccurred(), out)
			out, err = k.Delete("ns", namespace)
			Expect(err).ToNot(HaveOccurred(), out)
			out, err = k.Namespace("default").Delete("configmap", "configmap-values")
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	When("fleet.yaml makes use of valuesFrom", func() {
		Context("referencing a secret as well as a configmap", func() {
			It("all referenced resources are available as values to the chart", func() {
				Eventually(func() bool {
					_, err := kw.Get("configmap", "result")
					return err == nil
				}).Should(BeTrue())

				out, err := kw.Get("configmap", "result", "-o", "jsonpath={.data}")
				Expect(err).ToNot(HaveOccurred(), out)
				result := map[string]string{}
				err = json.Unmarshal([]byte(out), &result)
				Expect(err).ToNot(HaveOccurred())

				Expect(result).To(HaveKeyWithValue("name", "secret overrides values from fleet.yaml"))
				Expect(result).To(HaveKeyWithValue("secret", "xyz secret"))
				Expect(result).To(HaveKeyWithValue("config", "config option"))
				Expect(result).To(HaveKeyWithValue("fleetyaml", "from fleet.yaml"))
				Expect(result).To(HaveKeyWithValue("englishname", "secret override"))
				Expect(result).To(HaveKeyWithValue("optionconfigmap", "configmap option"))
				Expect(result).To(HaveKeyWithValue("optionsecret", "secret option"))
			})
		})
	})
})
