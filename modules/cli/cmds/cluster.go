package cmds

import (
	"fmt"
	"sort"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/rancher/wrangler/pkg/condition"

	"github.com/sirupsen/logrus"

	"github.com/rancher/wrangler/pkg/merr"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	command "github.com/rancher/wrangler-cli"
	"github.com/rancher/wrangler-cli/pkg/table"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewCluster() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage registered clusters",
	}
	cmd.AddCommand(
		NewClusterLS(),
		NewClusterCreate(),
		NewClusterDelete(),
	)
	return cmd
}

func NewClusterLS() *cobra.Command {
	return command.Command(&ClusterLS{}, cobra.Command{
		Use:   "ls",
		Short: "List registered",
	})
}

type ClusterLS struct {
	table.Args
	AllNamespaces bool `usage:"all namespaces" short:"A"`
}

func (l *ClusterLS) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	ns := c.Namespace
	if l.AllNamespaces {
		ns = ""
	}

	cols := [][]string{
		{"NAMESPACE", "Namespace"},
		{"NAME", "Name"},
		{"READY-BUNDLES", "{{bundles .}}"},
		{"READY-NODES", "{{nodes .}}"},
		{"A-NODE", "{{anode . }}"},
		{"LAST-SEEN", "Status.Agent.LastSeen"},
		{"STATUS", "{{status .}}"},
	}

	if !l.AllNamespaces {
		cols = cols[1:]
	}

	writer := table.NewWriter(cols, ns, l.Quiet, l.Format)
	writer.AddFormatFunc("bundles", func(cluster *fleet.Cluster) string {
		return fmt.Sprintf("%d/%d", cluster.Status.Summary.Ready, cluster.Status.Summary.DesiredReady)
	})
	writer.AddFormatFunc("nodes", func(cluster *fleet.Cluster) string {
		return fmt.Sprintf("%d/%d", cluster.Status.Agent.ReadyNodes,
			cluster.Status.Agent.NonReadyNodes+
				cluster.Status.Agent.ReadyNodes)
	})
	writer.AddFormatFunc("anode", func(cluster *fleet.Cluster) string {
		if len(cluster.Status.Agent.ReadyNodeNames) > 0 {
			return cluster.Status.Agent.ReadyNodeNames[0]
		}
		if len(cluster.Status.Agent.NonReadyNodeNames) > 0 {
			return cluster.Status.Agent.NonReadyNodeNames[0]
		}
		return ""
	})
	writer.AddFormatFunc("status", func(cluster *fleet.Cluster) string {
		return condition.Cond("Ready").GetMessage(cluster)
	})

	clusters, err := c.Fleet.Cluster().List(ns, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	sort.Slice(clusters.Items, func(i, j int) bool {
		return clusters.Items[i].CreationTimestamp.Before(&clusters.Items[j].CreationTimestamp)
	})

	for _, obj := range clusters.Items {
		writer.Write(&obj)
	}

	return writer.Close()
}

func NewClusterDelete() *cobra.Command {
	return command.Command(&ClusterDelete{}, cobra.Command{
		Use:   "unregister",
		Short: "Unregister a cluster from fleet",
		Args:  cobra.ArbitraryArgs,
	})
}

type ClusterDelete struct {
}

func (l *ClusterDelete) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	var errors []error
	for _, arg := range args {
		err := c.Fleet.Cluster().Delete(c.Namespace, arg, nil)
		if err == nil {
			fmt.Println(arg)
		} else {
			logrus.Errorf("failed to delete %s: %v", arg, err)
			errors = append(errors, err)
		}
	}

	return merr.NewErrors(errors...)
}

func NewClusterCreate() *cobra.Command {
	return command.Command(&ClusterCreate{}, cobra.Command{
		Use:   "create [flags] SECRET_NAME",
		Short: "Create a new cluster from a kubeconfig secret",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("the secret containing the kubeconfig must be passed as the first arguement")
			}
			return nil
		},
	})
}

type ClusterCreate struct {
	Name           string `usage:"cluster name (default: random)"`
	ValidateSecret bool   `usage:"Validate that the secret exists and is valid" default:"true"`
}

func (l *ClusterCreate) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    c.Namespace,
			GenerateName: "cluster-",
		},
		Spec: fleet.ClusterSpec{
			KubeConfigSecret: args[0],
		},
	}
	if l.Name != "" {
		cluster.GenerateName = ""
		cluster.Name = l.Name
	}

	secret, err := c.Core.Secret().Get(c.Namespace, args[0], metav1.GetOptions{})
	if err == nil {
		if v, ok := secret.Data["value"]; ok {
			err = fmt.Errorf("failed to find key \"value\" in secret %s/%s", c.Namespace, args[0])
		} else if _, marshallErr := clientcmd.RESTConfigFromKubeConfig(v); marshallErr != nil {
			err = fmt.Errorf("failed to read kubeconfig in secret %s/%s: %w", c.Namespace, args[0], marshallErr)
		}
	} else {
		err = fmt.Errorf("failed to find secret %s/%s: %w", c.Namespace, args[0], err)
	}
	if err != nil {
		if l.ValidateSecret {
			return err
		} else {
			logrus.Warn(err)
		}
	}

	t, err := c.Fleet.Cluster().Create(cluster)
	if err == nil {
		fmt.Println(t.Name)
	}
	return err
}
