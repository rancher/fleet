package poll

import (
	"context"
	"fmt"
	"strings"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Watcher interface {
	StartBackgroundSync(ctx context.Context)
	Finish()
	Restart(ctx context.Context)
	UpdateGitRepo(gitRepo v1alpha1.GitRepo)
	GetSyncInterval() float64
}

// Handler handles all the watches for the git repositories. These watches are pulling the latest commit every syncPeriod.
type Handler struct {
	client      client.Client
	watches     map[string]Watcher
	createWatch func(gitRepo v1alpha1.GitRepo, client client.Client) Watcher // this func creates a watch. It's a struct field, so it can be replaced for a mock in unit tests.
	log         logr.Logger
}

func NewHandler(client client.Client) *Handler {
	return &Handler{
		client:      client,
		watches:     make(map[string]Watcher),
		createWatch: NewWatch,
		log:         ctrl.Log.WithName("git-latest-commit-poll-handler"),
	}
}

// AddOrModifyGitRepoWatch adds a new watch for the gitrepo if no watch was already present.
// It updates the existing watch for this gitrepo if present.
func (h *Handler) AddOrModifyGitRepoWatch(ctx context.Context, gitRepo v1alpha1.GitRepo) {
	key := getKey(gitRepo)
	watch, found := h.watches[key]
	if !found {
		h.watches[key] = h.createWatch(gitRepo, h.client)
		h.watches[key].StartBackgroundSync(ctx)
	} else {
		oldSyncInterval := watch.GetSyncInterval()
		watch.UpdateGitRepo(gitRepo)

		gitRepoSyncInterval := 0.0
		if pi := gitRepo.Spec.PollingInterval; pi != nil {
			gitRepoSyncInterval = pi.Seconds()
		}

		if oldSyncInterval != gitRepoSyncInterval {
			watch.Restart(ctx)
		}
	}
}

// CleanUpWatches removes all watches whose gitrepo is not present in the cluster.
func (h *Handler) CleanUpWatches(ctx context.Context) {
	var gitRepo v1alpha1.GitRepo
	for key, watch := range h.watches {
		namespacedName, err := getTypeNamespaceFromKey(key)
		if err != nil {
			h.log.Error(err, "can't get namespacedName", key)
		}
		if err = h.client.Get(ctx, namespacedName, &gitRepo); errors.IsNotFound(err) {
			watch.Finish()
			delete(h.watches, key)
		}
	}
}

func getTypeNamespaceFromKey(key string) (types.NamespacedName, error) {
	split := strings.Split(key, "-")
	if len(split) < 2 {
		return types.NamespacedName{}, fmt.Errorf("invalid key")
	}
	return types.NamespacedName{
		Namespace: split[1],
		Name:      split[0],
	}, nil
}

func getKey(gitRepo v1alpha1.GitRepo) string {
	return gitRepo.Name + "-" + gitRepo.Namespace
}
