// Package singlecluster contains e2e tests deploying to a single cluster. The tests use kubectl to apply manifests. Expectations are verified by checking cluster resources. Assets refer to the https://github.com/rancher/fleet-test-data git repo.
package singlecluster_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/rancher/fleet/e2e/testenv"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(testenv.FailAndGather)
	RunSpecs(t, "E2E Suite for Single-Cluster")
}

const (
	repoName = "repo"
)

var (
	env            *testenv.Env
	clientUpstream client.Client
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	SetDefaultEventuallyPollingInterval(time.Second)
	testenv.SetRoot("../..")

	env = testenv.New()
	clientUpstream = getClientForContext(env.Upstream)

	Expect(env.Namespace).To(Equal("fleet-local"), "The single-cluster test assets target the default clustergroup and only work in fleet-local")
})

func getClientForContext(contextName string) client.Client {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: contextName,
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := clientConfig.ClientConfig()
	Expect(err).ToNot(HaveOccurred())

	// Set up scheme
	schema := runtime.NewScheme()
	Expect(corev1.AddToScheme(schema)).ToNot(HaveOccurred())
	Expect(appsv1.AddToScheme(schema)).ToNot(HaveOccurred())
	Expect(clientgoscheme.AddToScheme(schema)).ToNot(HaveOccurred())
	Expect(fleet.AddToScheme(schema)).ToNot(HaveOccurred())

	// Create controller-runtime client
	c, err := client.New(restConfig, client.Options{Scheme: schema})
	Expect(err).ToNot(HaveOccurred())

	return c
}
