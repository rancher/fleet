package cmds

import (
	"fmt"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	command "github.com/rancher/wrangler-cli"
	"github.com/rancher/wrangler-cli/pkg/table"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewGitRepo() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gitrepo",
		Short: "Manage registered git repos",
	}
	cmd.AddCommand(
		NewGitRepoLS(),
		NewGitRepoAdd(),
		//NewGitRepoDelete(),
	)
	return cmd
}

func NewGitRepoLS() *cobra.Command {
	return command.Command(&GitRepoLS{}, cobra.Command{
		Use:   "ls",
		Short: "List git repos",
	})
}

type GitRepoLS struct {
	table.Args
	AllNamespaces bool `usage:"all namespaces" short:"A"`
}

func (l *GitRepoLS) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	ns := c.Namespace
	if l.AllNamespaces {
		ns = ""
	}

	gitRepos, err := c.Fleet.GitRepo().List(ns, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	cols := [][]string{
		{"NAMESPACE", "Namespace"},
		{"NAME", "Name"},
		{"CREATED", "{{.CreationTimestamp | ago}}"},
		{"REPO", "Spec.Repo"},
		{"BRANCH/REV", "{{branch .}}"},
		{"COMMIT", "Status.Commit"},
	}

	if !l.AllNamespaces {
		cols = cols[1:]
	}

	writer := table.NewWriter(cols, ns, l.Quiet, l.Format)
	writer.AddFormatFunc("branch", func(obj interface{}) string {
		if repo, ok := obj.(*fleet.GitRepo); ok {
			if repo.Spec.Revision != "" {
				return repo.Spec.Revision
			}
			if repo.Spec.Branch == "" {
				return "master"
			}
			return repo.Spec.Branch
		}
		return ""
	})

	sort.Slice(gitRepos.Items, func(i, j int) bool {
		return gitRepos.Items[i].CreationTimestamp.Before(&gitRepos.Items[j].CreationTimestamp)
	})

	for _, obj := range gitRepos.Items {
		writer.Write(&obj)
	}

	return writer.Close()
}

//func NewGitRepoDelete() *cobra.Command {
//	return command.Command(&GitRepoDelete{}, cobra.Command{
//		Use:   "delete",
//		Short: "Delete cluster registration gitRepo(s)",
//		Args:  cobra.ArbitraryArgs,
//	})
//}
//
//type GitRepoDelete struct {
//}
//
//func (l *GitRepoDelete) Run(cmd *cobra.Command, args []string) error {
//	c, err := Client.Get()
//	if err != nil {
//		return err
//	}
//
//	var errors []error
//	for _, arg := range args {
//		err := c.Fleet.ClusterRegistrationGitRepo().Delete(c.Namespace, arg, nil)
//		if err == nil {
//			fmt.Println(arg)
//		} else {
//			logrus.Errorf("failed to delete %s: %v", arg, err)
//			errors = append(errors, err)
//		}
//	}
//
//	return merr.NewErrors(errors...)
//}
//
func NewGitRepoAdd() *cobra.Command {
	return command.Command(&GitRepoAdd{}, cobra.Command{
		Use: "add [flags] URL",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("a URL to the git repo is required as a parameter")
			}
			return nil
		},
		Short: "Add a git repository to watch",
	})
}

type GitRepoAdd struct {
	Name     string   `usage:"The name to assign to the git repo"`
	Branch   string   `usage:"The branch to watch" short:"b"`
	Revision string   `usage:"A specific commit or tag to watch" short:"r"`
	Secret   string   `usage:"Secret name containing the credentials used to perform the git clone"`
	Dirs     []string `usage:"Directories in the git repo that contains bundles. Supports path globbing" short:"d"`
}

func (l *GitRepoAdd) Run(cmd *cobra.Command, args []string) error {
	c, err := Client.Get()
	if err != nil {
		return err
	}

	gitRepo := &fleet.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    c.Namespace,
			GenerateName: "repo-",
		},
		Spec: fleet.GitRepoSpec{
			Repo:             args[0],
			Branch:           l.Branch,
			Revision:         l.Revision,
			ClientSecretName: l.Secret,
			BundleDirs:       l.Dirs,
		},
	}
	if l.Name != "" {
		gitRepo.GenerateName = ""
		gitRepo.Name = l.Name
	}

	t, err := c.Fleet.GitRepo().Create(gitRepo)
	if err == nil {
		fmt.Println(t.Name)
	}
	return err
}
