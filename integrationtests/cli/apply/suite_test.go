package apply

import (
	"context"
	"testing"

	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/schemes"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	buf    *gbytes.Buffer
	scheme = runtime.NewScheme()
)

func TestFleetApply(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Apply Suite")
}

var _ = BeforeSuite(func() {
	// scheme for k8sClient
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	Expect(schemes.Register(v1alpha1.AddToScheme)).NotTo(HaveOccurred())
})

// simulates fleet cli execution
func fleetApply(name string, dirs []string, options apply.Options) error {
	buf = gbytes.NewBuffer()
	options.Output = buf
	ctrl := gomock.NewController(GinkgoT())
	c := mocks.NewMockK8sClient(ctrl)
	return apply.CreateBundles(context.Background(), c, nil, name, dirs, options)
}

// simulates fleet cli execution in driven mode
func fleetApplyDriven(name string, dirs []string, options apply.Options) error {
	buf = gbytes.NewBuffer()
	options.DrivenScan = true
	options.Output = buf
	options.DrivenScanSeparator = ":"
	ctrl := gomock.NewController(GinkgoT())
	c := mocks.NewMockK8sClient(ctrl)
	return apply.CreateBundlesDriven(context.Background(), c, nil, name, dirs, options)
}

// simulates fleet cli online execution, with mocked client
func fleetApplyOnline(c client.Client, name string, dirs []string, options apply.Options) error {
	return apply.CreateBundles(context.Background(), c, nil, name, dirs, options)
}
