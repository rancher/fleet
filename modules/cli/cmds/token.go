package cmds

import (
	"fmt"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/wrangler/pkg/merr"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	command "github.com/rancher/wrangler-cli"
	"github.com/rancher/wrangler-cli/pkg/table"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewToken() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage cluster registration tokens",
	}
	cmd.AddCommand(
		NewTokenLS(),
		NewTokenCreate(),
		NewTokenDelete(),
	)
	return cmd
}

func NewTokenLS() *cobra.Command {
	return command.Command(&TokenLS{}, cobra.Command{
		Use:   "ls",
		Short: "List cluster registration tokens",
	})
}

type TokenLS struct {
	table.Args
	AllNamespaces bool `usage:"all namespaces" short:"A"`
}

func (l *TokenLS) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	ns := c.Namespace
	if l.AllNamespaces {
		ns = ""
	}

	tokens, err := c.Fleet.ClusterRegistrationToken().List(ns, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	cols := [][]string{
		{"NAMESPACE", "Namespace"},
		{"NAME", "Name"},
		{"CREATED", "{{.CreationTimestamp | ago}}"},
		{"EXPIRES", "Status.Expires"},
		{"SECRET", "Status.SecretName"},
	}

	if !l.AllNamespaces {
		cols = cols[1:]
	}

	writer := table.NewWriter(cols, ns, l.Quiet, l.Format)

	sort.Slice(tokens.Items, func(i, j int) bool {
		return tokens.Items[i].CreationTimestamp.Before(&tokens.Items[j].CreationTimestamp)
	})

	for _, obj := range tokens.Items {
		writer.Write(&obj)
	}

	return writer.Close()
}

func NewTokenDelete() *cobra.Command {
	return command.Command(&TokenDelete{}, cobra.Command{
		Use:   "delete",
		Short: "Delete cluster registration token(s)",
		Args:  cobra.ArbitraryArgs,
	})
}

type TokenDelete struct {
}

func (l *TokenDelete) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	var errors []error
	for _, arg := range args {
		err := c.Fleet.ClusterRegistrationToken().Delete(c.Namespace, arg, nil)
		if err == nil {
			fmt.Println(arg)
		} else {
			logrus.Errorf("failed to delete %s: %v", arg, err)
			errors = append(errors, err)
		}
	}

	return merr.NewErrors(errors...)
}

func NewTokenCreate() *cobra.Command {
	return command.Command(&TokenCreate{}, cobra.Command{
		Use:   "create",
		Short: "Create a new cluster registration tokens",
	})
}

type TokenCreate struct {
	Name string `usage:"token name (default: random)"`
	TTL  string `usage:"How long the generated registration token is valid, 0 means forever" default:"1440m" short:"t"`
}

func (l *TokenCreate) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	ttlSeconds := 0
	if l.TTL != "" && l.TTL != "0" {
		ttl, err := time.ParseDuration(l.TTL)
		if err != nil {
			return err
		}
		ttlSeconds = int(ttl.Seconds())
	}

	token := &fleet.ClusterRegistrationToken{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    c.Namespace,
			GenerateName: "token-",
		},
		Spec: fleet.ClusterRegistrationTokenSpec{
			TTLSeconds: ttlSeconds,
		},
	}
	if l.Name != "" {
		token.GenerateName = ""
		token.Name = l.Name
	}

	t, err := c.Fleet.ClusterRegistrationToken().Create(token)
	if err == nil {
		fmt.Println(t.Name)
	}
	return err
}
