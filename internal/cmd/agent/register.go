package agent

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/agent/register"
	"github.com/rancher/wrangler/v2/pkg/kubeconfig"
	"github.com/rancher/wrangler/v2/pkg/leader"
	"github.com/rancher/wrangler/v2/pkg/ratelimit"
)

func NewRegister() *cobra.Command {
	cmd := command.Command(&Register{}, cobra.Command{
		Use:   "register [flags]",
		Short: "Register agent with an upstream cluster",
	})
	return cmd
}

type Register struct {
	UpstreamOptions
}

func (r *Register) Run(cmd *cobra.Command, args []string) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(r.Kubeconfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	// try to claim leadership lease without rate limiting
	localConfig := rest.CopyConfig(kc)
	localConfig.RateLimiter = ratelimit.None
	k8s, err := kubernetes.NewForConfig(localConfig)
	if err != nil {
		return err
	}

	logrus.Printf("starting registration on upstream cluster in namespace %s", r.Namespace)

	ctx, cancel := context.WithCancel(cmd.Context())
	leader.RunOrDie(ctx, r.Namespace, "fleet-agent-register-lock", k8s, func(ctx context.Context) {
		// try to register with upstream fleet controller by obtaining
		// a kubeconfig for the upstream cluster
		agentInfo, err := register.Register(ctx, r.Namespace, kc)
		if err != nil {
			logrus.Fatal(err)
		}

		ns, _, err := agentInfo.ClientConfig.Namespace()
		if err != nil {
			logrus.Fatal(err)
		}

		_, err = agentInfo.ClientConfig.ClientConfig()
		if err != nil {
			logrus.Fatal(err)
		}

		logrus.Printf("successfully registered with upstream cluster in namespace %s", ns)
		cancel()
	})

	return nil
}
