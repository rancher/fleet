package durationvalidation

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	cancel    context.CancelFunc
	ctx       context.Context
	cfg       *rest.Config
	k8sClient client.Client
	testenv   *envtest.Environment

	namespace = "duration-validation"
)

func TestDurationValidation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CRD Duration Validation Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())
	testenv = utils.NewEnvTest("../../..")

	var err error
	cfg, err = utils.StartTestEnv(testenv)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = utils.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	})).To(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testenv.Stop()).ToNot(HaveOccurred())
})
