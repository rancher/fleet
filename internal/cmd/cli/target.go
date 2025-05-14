package cli

import (
	"flag"
	"fmt"
	"os"
	"reflect"

	"github.com/spf13/cobra"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"
)

var (
	zopts  = zap.Options{Development: true}
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

// NewTarget returns a subcommand to print available targets for a bundle
func NewTarget() *cobra.Command {
	cmd := command.Command(&Target{}, cobra.Command{
		Short: "Print available targets for a bundle",
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

type Target struct {
	BundleFile    string `usage:"Location of the Bundle resource yaml" short:"b"`
	DumpInputList bool   `usage:"Dump the live resources, which impact targeting, like clusters, as YAML" short:"l"`

	Namespace string `usage:"Override the namespace of the bundle. Targeting searches this namespace for clusters." short:"n"`
}

func (t *Target) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	if t.BundleFile == "" {
		return cmd.Help()
	}

	b, err := os.ReadFile(t.BundleFile)
	if err != nil {
		return err
	}
	bundle := &v1alpha1.Bundle{}
	err = yaml.Unmarshal(b, bundle)
	if err != nil {
		return err
	}

	empty := &v1alpha1.Bundle{TypeMeta: metav1.TypeMeta{APIVersion: "v1"}}
	if reflect.DeepEqual(bundle, empty) {
		return fmt.Errorf("failed to read bundle from file, bundle is empty")
	}

	if t.Namespace != "" {
		bundle.Namespace = t.Namespace
	}

	manifest := manifest.FromBundle(bundle)
	manifestID, err := manifest.ID()
	if err != nil {
		return err
	}

	cfg := ctrl.GetConfigOrDie()
	client, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	builder := target.New(client, client)
	matchedTargets, err := builder.Targets(ctx, bundle, manifestID)
	if err != nil {
		return err
	}

	if t.DumpInputList {
		// remove managed fields
		for _, target := range matchedTargets {
			target.Cluster.SetManagedFields(nil)
			for _, cg := range target.ClusterGroups {
				cg.SetManagedFields(nil)
			}
		}
		b, err := yaml.Marshal(matchedTargets)
		if err != nil {
			return err
		}
		cmd.PrintErrln(string(b))
	}

	// output manifest/content resource
	data, err := manifest.Content()
	if err != nil {
		return err
	}
	digest, err := manifest.SHASum()
	if err != nil {
		return err
	}

	compressed, err := content.Gzip(data)
	if err != nil {
		return err
	}

	content := v1alpha1.Content{
		ObjectMeta: metav1.ObjectMeta{
			Name: manifestID,
		},
		Content:   compressed,
		SHA256Sum: digest,
	}
	content.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Content"))

	b, err = yaml.Marshal(content)
	if err != nil {
		return err
	}
	cmd.Println("---")
	cmd.Println(string(b))

	// Needs to be set to print all targets. UpdatePartitions will only
	// create this many deployments if the bundle is new.
	bundle.Status.MaxNew = len(matchedTargets)

	if err := target.UpdatePartitions(&bundle.Status, matchedTargets); err != nil {
		return err
	}
	for _, target := range matchedTargets {
		if target.Deployment == nil {
			continue
		}
		bd := target.BundleDeployment()
		bd.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("BundleDeployment"))

		b, err := yaml.Marshal(bd)
		if err != nil {
			return err
		}

		cmd.Println("---")
		cmd.Println(string(b))
	}

	return nil
}
