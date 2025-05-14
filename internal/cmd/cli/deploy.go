package cli

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"reflect"

	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/runtime"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	wyaml "github.com/rancher/wrangler/v3/pkg/yaml"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"
)

const defaultNamespace = "default"

// NewDeploy returns a subcommand to deploy a bundledeployment/content resource to a cluster.
func NewDeploy() *cobra.Command {
	cmd := command.Command(&Deploy{}, cobra.Command{
		Short: "Deploy a bundledeployment/content resource to a cluster, by creating a Helm release. This will not deploy the bundledeployment/content resources directly to the cluster.",
	})
	cmd.SetOut(os.Stdout)

	// add command line flags from zap and controller-runtime, which use
	// goflags and convert them to pflags
	fs := flag.NewFlagSet("", flag.ExitOnError)
	zopts.BindFlags(fs)
	ctrl.RegisterFlags(fs)
	cmd.Flags().AddGoFlagSet(fs)
	return cmd
}

type Deploy struct {
	InputFile   string `usage:"Location of the YAML file containing the content and the bundledeployment resource" short:"i"`
	DryRun      bool   `usage:"Print the resources that would be deployed, but do not actually deploy them" short:"d"`
	Namespace   string `usage:"Set the default namespace. Deploy helm chart into this namespace." short:"n"`
	KubeVersion string `usage:"For dry runs, sets the Kubernetes version to assume when validating Chart Kubernetes version constraints."`

	// AgentNamespace is set as an annotation on the chart.yaml in the helm release. Fleet-agent will manage charts with a matching label.
	AgentNamespace string `usage:"Set the agent namespace, normally cattle-fleet-system. If set, fleet agent will garbage collect the helm release, i.e. delete it if the bundledeployment is missing." short:"a"`
}

func (d *Deploy) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	if d.InputFile == "" {
		return cmd.Help()
	}

	b, err := os.ReadFile(d.InputFile)
	if err != nil {
		return err
	}

	c := &v1alpha1.Content{}
	bd := &v1alpha1.BundleDeployment{}
	objs, err := wyaml.ToObjects(bytes.NewBuffer(b))
	if err != nil {
		return err
	}

	// position of the content and bundledeployment resources in the file is not guaranteed
	for _, obj := range objs {
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case "Content":
			un, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			if err != nil {
				return err
			}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(un, c)
			if err != nil {
				return err
			}
		case "BundleDeployment":
			un, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			if err != nil {
				return err
			}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(un, bd)
			if err != nil {
				return err
			}
		}
	}

	emptyContent := &v1alpha1.Content{}
	if reflect.DeepEqual(c, emptyContent) {
		return fmt.Errorf("failed to read content resource from file")
	}

	emptyBD := &v1alpha1.BundleDeployment{}
	if reflect.DeepEqual(bd, emptyBD) {
		return fmt.Errorf("failed to read bundledeployment resource from file")
	}

	data, err := content.GUnzip(c.Content)
	if err != nil {
		return err
	}
	manifest, err := manifest.FromJSON(data, c.SHA256Sum)
	if err != nil {
		return err
	}

	if d.DryRun {
		rel, err := helmdeployer.Template(ctx, bd.Name, manifest, bd.Spec.Options, d.KubeVersion)
		if err != nil {
			return err
		}

		return printRelease(cmd, rel)
	}

	cfg := ctrl.GetConfigOrDie()
	client, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	namespace := defaultNamespace
	if d.Namespace != "" {
		namespace = d.Namespace
	}

	deployer := helmdeployer.New(
		d.AgentNamespace,
		namespace,
		defaultNamespace,
		d.AgentNamespace,
	)

	if kubeconfig := flag.Lookup("kubeconfig").Value.String(); kubeconfig != "" {
		// set KUBECONFIG env var so helm can find it
		os.Setenv("KUBECONFIG", kubeconfig)
	}

	// Note: deployer does not check the bundles dependencies
	err = deployer.Setup(ctx, client, cli.New().RESTClientGetter())
	if err != nil {
		return err
	}

	rel, err := deployer.Deploy(ctx, bd.Name, manifest, bd.Spec.Options)
	if err != nil {
		return err
	}

	return printRelease(cmd, rel)
}

func printRelease(cmd *cobra.Command, rel *release.Release) error {
	resources, err := wyaml.ToObjects(bytes.NewBufferString(rel.Manifest))
	if err != nil {
		return err
	}

	for _, h := range rel.Hooks {
		hookResources, err := wyaml.ToObjects(bytes.NewBufferString(h.Manifest))
		if err != nil {
			return err
		}
		resources = append(resources, hookResources...)
	}

	b, err := yaml.Marshal(resources)
	if err != nil {
		return err
	}
	cmd.Println(string(b))

	return nil
}
