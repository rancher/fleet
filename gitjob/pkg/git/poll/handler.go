package poll

import (
	"context"
	"fmt"
	"strings"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"

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
	UpdateGitJob(gitJob v1.GitJob)
	GetSyncInterval() int
}

// Handler handles all the watches for the git repositories. These watches are pulling the latest commit every syncPeriod.
type Handler struct {
	client      client.Client
	watches     map[string]Watcher
	createWatch func(gitJob v1.GitJob, client client.Client) Watcher // this func creates a watch. It's a struct field, so it can be replaced for a mock in unit tests.
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

// AddOrModifyGitRepoWatch adds a new watch for the gitjob if no watch was already present.
// It updates the existing watch for this gitjob if present.
func (h *Handler) AddOrModifyGitRepoWatch(ctx context.Context, gitJob v1.GitJob) {
	key := getKey(gitJob)
	watch, found := h.watches[key]
	if !found {
		h.watches[key] = h.createWatch(gitJob, h.client)
		h.watches[key].StartBackgroundSync(ctx)
	} else {
		oldSyncInterval := watch.GetSyncInterval()
		watch.UpdateGitJob(gitJob)
		if oldSyncInterval != gitJob.Spec.SyncInterval {
			watch.Restart(ctx)
		}
	}
}

// CleanUpWatches removes all watches whose gitjob is not present in the cluster.
func (h *Handler) CleanUpWatches(ctx context.Context) {
	var gitJob v1.GitJob
	for key, watch := range h.watches {
		namespacedName, err := getTypeNamespaceFromKey(key)
		if err != nil {
			h.log.Error(err, "can't get namespacedName", key)
		}
		if err = h.client.Get(ctx, namespacedName, &gitJob); errors.IsNotFound(err) {
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

func getKey(gitJob v1.GitJob) string {
	return gitJob.Name + "-" + gitJob.Namespace
}
