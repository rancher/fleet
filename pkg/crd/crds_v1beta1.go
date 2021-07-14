package crd

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/crd"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

func CreateV1Beta1(ctx context.Context, cfg *rest.Config) error {
	factory, err := crd.NewFactoryFromClient(cfg)
	if err != nil {
		return err
	}

	return factory.BatchCreateCRDsV1Beta1(ctx, ListV1Beta1()...).BatchWait()
}

func ObjectsV1Beta1() (result []runtime.Object, err error) {
	for _, crdDef := range ListV1Beta1() {
		crd, err := crdDef.ToCustomResourceDefinitionV1Beta1()
		if err != nil {
			return nil, err
		}
		result = append(result, crd)
	}
	return
}

func ListV1Beta1() []crd.CRD {
	return []crd.CRD{
		newCRD(&fleet.Bundle{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumnV1Beta1("BundleDeployments-Ready", ".status.display.readyClusters").
				WithColumnV1Beta1("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.BundleDeployment{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumnV1Beta1("Deployed", ".status.display.deployed").
				WithColumnV1Beta1("Monitored", ".status.display.monitored").
				WithColumnV1Beta1("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.BundleNamespaceMapping{}, func(c crd.CRD) crd.CRD {
			return c
		}),
		newCRD(&fleet.ClusterGroup{}, func(c crd.CRD) crd.CRD {
			return c.
				WithCategories("fleet").
				WithColumnV1Beta1("Clusters-Ready", ".status.display.readyClusters").
				WithColumnV1Beta1("Bundles-Ready", ".status.display.readyBundles").
				WithColumnV1Beta1("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.Cluster{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumnV1Beta1("Bundles-Ready", ".status.display.readyBundles").
				WithColumnV1Beta1("Nodes-Ready", ".status.display.readyNodes").
				WithColumnV1Beta1("Sample-Node", ".status.display.sampleNode").
				WithColumnV1Beta1("Last-Seen", ".status.agent.lastSeen").
				WithColumnV1Beta1("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.ClusterRegistrationToken{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumnV1Beta1("Secret-Name", ".status.secretName")
		}),
		newCRD(&fleet.GitRepo{}, func(c crd.CRD) crd.CRD {
			return c.
				WithCategories("fleet").
				WithColumnV1Beta1("Repo", ".spec.repo").
				WithColumnV1Beta1("Commit", ".status.commit").
				WithColumnV1Beta1("BundleDeployments-Ready", ".status.display.readyBundleDeployments").
				WithColumnV1Beta1("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.ClusterRegistration{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumnV1Beta1("Cluster-Name", ".status.clusterName").
				WithColumnV1Beta1("Labels", ".spec.clusterLabels")
		}),
		newCRD(&fleet.GitRepoRestriction{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumnV1Beta1("Default-ServiceAccount", ".defaultServiceAccount").
				WithColumnV1Beta1("Allowed-ServiceAccounts", ".allowedServiceAccounts")
		}),
		newCRD(&fleet.Content{}, func(c crd.CRD) crd.CRD {
			c.NonNamespace = true
			c.Status = false
			return c
		}),
		newCRD(&fleet.ImageScan{}, func(c crd.CRD) crd.CRD {
			return c.WithCategories("fleet").
				WithColumnV1Beta1("Repository", ".spec.image").
				WithColumnV1Beta1("Latest", ".status.latestTag")
		}),
	}
}