//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/getter_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/cli/apply Getter
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/fleet_controller_mock.go -package=mocks -mock_names=Interface=FleetInterface github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1 Interface
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/core_controller_mock.go -package=mocks -mock_names=Interface=CoreInterface github.com/rancher/wrangler/v2/pkg/generated/controllers/core/v1 Interface
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/rbac_controller_mock.go -package=mocks -mock_names=Interface=RBACInterface github.com/rancher/wrangler/v2/pkg/generated/controllers/rbac/v1 Interface
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/apply_mock.go -package=mocks github.com/rancher/wrangler/v2/pkg/apply Apply

package apply

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/integrationtests/mocks"
	"github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/generic/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Fleet apply online", Ordered, func() {
	var (
		dirs    []string
		name    string
		options apply.Options
	)
	JustBeforeEach(func() {
		//Implementing all the prerequisites for the test
		ctrl := gomock.NewController(GinkgoT())
		getter := mocks.NewMockGetter(ctrl)
		client := client.Client{
			Fleet:     mocks.NewFleetInterface(ctrl),
			Core:      mocks.NewCoreInterface(ctrl),
			RBAC:      mocks.NewRBACInterface(ctrl),
			Apply:     mocks.NewMockApply(ctrl),
			Namespace: "foo",
		}
		bundleController := fake.NewMockControllerInterface[*fleet.Bundle, *fleet.BundleList](ctrl)
		fleetMock := client.Fleet.(*mocks.FleetInterface)

		//bundle in the cluster
		oldBundle := &fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"new":         "fleet-label2",
					"new_changed": "fleet_label_changed",
				},
				Namespace: "foo",
				Name:      "test_labels",
			},
		}

		//bundle in the fleet.yaml file some values are autofulfilled in the implementation (this is why there are not only the labels)
		newBundle := &fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"new": "fleet-label2",
				},
				Namespace: "foo",
				Name:      "test_labels",
			},
			Spec: fleet.BundleSpec{
				Resources: []fleet.BundleResource{
					{
						Name:    "fleet.yaml",
						Content: "labels:\n  new: fleet-label2",
					},
				},
				Targets: []fleet.BundleTarget{
					{
						Name:         "default",
						ClusterGroup: "default",
					},
				},
			},
		}

		getter.EXPECT().GetNamespace().Return("foo").AnyTimes()
		getter.EXPECT().Get().Return(&client, nil).AnyTimes()
		fleetMock.EXPECT().Bundle().Return(bundleController).AnyTimes()
		bundleController.EXPECT().Get("foo", gomock.Any(), gomock.Any()).Return(oldBundle, nil)
		//The core of the test is the Update() method because it will verify if the Labels are correctly updated
		bundleController.EXPECT().Update(newBundle).Return(newBundle, nil)
		bundleController.EXPECT().List(gomock.Any(), gomock.Any()).Return(&fleet.BundleList{}, nil)

		err := fleetApplyOnline(getter, name, dirs, options)
		Expect(err).NotTo(HaveOccurred())

	})

	When("Folder contains a fleet.yaml fulfilled with less labels than the bundle which we want to update", func() {
		BeforeEach(func() {
			name = "labels_update"
			//The folder contains a fleet.yaml with only one label to simulate the case when we want to delete a label
			dirs = []string{cli.AssetsPath + "labels_update"}
		})
		It("should correctly update the labels from fleet.yaml", func() {
			//fleetApplyOnline method do not return any value of the updated bundle, so we cannot check the labels here
			//Instead, we try the good updating of the bundle using the Update() method in the JustBeforeEach
		})
	})

})
