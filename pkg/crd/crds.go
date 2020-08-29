package crd

import (
	"context"
	"io"
	"os"
	"path/filepath"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/crd"
	"github.com/rancher/wrangler/pkg/yaml"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func WriteFile(filename string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	return Print(f)
}

func Print(out io.Writer) error {
	obj, err := Objects()
	if err != nil {
		return err
	}

	data, err := yaml.Export(obj...)
	if err != nil {
		return err
	}

	_, err = out.Write(data)
	return err
}

func Objects() (result []runtime.Object, err error) {
	for _, crdDef := range List() {
		crd, err := crdDef.ToCustomResourceDefinition()
		if err != nil {
			return nil, err
		}
		result = append(result, &crd)
	}
	return
}

func List() []crd.CRD {
	return []crd.CRD{
		newCRD(&fleet.Bundle{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("BundleDeployments-Ready", ".status.display.readyClusters").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.BundleDeployment{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Deployed", ".status.display.deployed").
				WithColumn("Monitored", ".status.display.monitored").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.BundleNamespaceMapping{}, func(c crd.CRD) crd.CRD {
			return c
		}),
		newCRD(&fleet.ClusterGroup{}, func(c crd.CRD) crd.CRD {
			return c.
				WithCategories("fleet").
				WithColumn("Clusters-Ready", ".status.display.readyClusters").
				WithColumn("Bundles-Ready", ".status.display.readyBundles").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.Cluster{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Bundles-Ready", ".status.display.readyBundles").
				WithColumn("Nodes-Ready", ".status.display.readyNodes").
				WithColumn("Sample-Node", ".status.display.sampleNode").
				WithColumn("Last-Seen", ".status.agent.lastSeen").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.ClusterRegistrationToken{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Secret-Name", ".status.secretName")
		}),
		newCRD(&fleet.GitRepo{}, func(c crd.CRD) crd.CRD {
			return c.
				WithCategories("fleet").
				WithColumn("Repo", ".spec.repo").
				WithColumn("Commit", ".status.commit").
				WithColumn("BundleDeployments-Ready", ".status.display.readyBundleDeployments").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&fleet.ClusterRegistration{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Cluster-Name", ".status.clusterName").
				WithColumn("Labels", ".spec.clusterLabels")
		}),
		newCRD(&fleet.Content{}, func(c crd.CRD) crd.CRD {
			c.NonNamespace = true
			c.Status = false
			return c
		}),
	}
}

func Create(ctx context.Context, cfg *rest.Config) error {
	factory, err := crd.NewFactoryFromClient(cfg)
	if err != nil {
		return err
	}

	return factory.BatchCreateCRDs(ctx, List()...).BatchWait()
}

func newCRD(obj interface{}, customize func(crd.CRD) crd.CRD) crd.CRD {
	crd := crd.CRD{
		GVK: schema.GroupVersionKind{
			Group:   "fleet.cattle.io",
			Version: "v1alpha1",
		},
		Status:       true,
		SchemaObject: obj,
	}
	if customize != nil {
		crd = customize(crd)
	}
	return crd
}
