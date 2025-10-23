package doctor

import (
	"context"
	"os"
	"testing"

	"github.com/rancher/fleet/internal/cmd/cli/doctor"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/schemes"
	"k8s.io/apimachinery/pkg/runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme = runtime.NewScheme()
)

func TestFleetDoctor(t *testing.T) {
	os.Setenv("KUBEBUILDER_ASSETS", "/home/tan/.local/share/kubebuilder-envtest/k8s/1.34.1-linux-amd64")
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Doctor Suite")
}

var _ = BeforeSuite(func() {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// scheme for k8sClient
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	Expect(schemes.Register(v1alpha1.AddToScheme)).NotTo(HaveOccurred())
})

// simulates fleet cli online execution, with mocked client
func fleetDoctor(d dynamic.Interface, path string) error {
	return doctor.CreateReport(context.Background(), d, path)
}
