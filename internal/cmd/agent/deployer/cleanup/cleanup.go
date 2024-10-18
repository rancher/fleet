package cleanup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/action"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/kv"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/merr"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"

	apierror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type HelmDeployer interface {
	NewListAction() *action.List
	ListDeployments(list helmdeployer.ListAction) ([]helmdeployer.DeployedBundle, error)
	DeleteRelease(ctx context.Context, deployed helmdeployer.DeployedBundle) error
	Delete(ctx context.Context, name string) error
}

type Cleanup struct {
	client           client.Client
	fleetNamespace   string
	defaultNamespace string
	helmDeployer     HelmDeployer
	cleanupOnce      sync.Once

	mapper meta.RESTMapper
	// localDynamicClient is a dynamic client for the cluster the agent is running on (local cluster).
	localDynamicClient *dynamic.DynamicClient

	garbageCollectionInterval time.Duration
}

func New(
	upstream client.Client,
	mapper meta.RESTMapper,
	localDynamicClient *dynamic.DynamicClient,
	deployer HelmDeployer,
	fleetNamespace string,
	defaultNamespace string,
	garbageCollectionInterval time.Duration,
) *Cleanup {
	if garbageCollectionInterval == 0 {
		garbageCollectionInterval = durations.GarbageCollect
	}

	return &Cleanup{
		client:                    upstream,
		mapper:                    mapper,
		localDynamicClient:        localDynamicClient,
		helmDeployer:              deployer,
		fleetNamespace:            fleetNamespace,
		defaultNamespace:          defaultNamespace,
		garbageCollectionInterval: garbageCollectionInterval,
	}
}

func (c *Cleanup) OldAgent(ctx context.Context, modifiedStatuses []fleet.ModifiedStatus) error {
	logger := log.FromContext(ctx).WithName("cleanup-old-agent")
	var errs []error
	for _, modified := range modifiedStatuses {
		if modified.Delete {
			gvk := schema.FromAPIVersionAndKind(modified.APIVersion, modified.Kind)
			mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				errs = append(errs, fmt.Errorf("mapping resource for %s for agent cleanup: %w", gvk, err))
				continue
			}

			logger.Info("Removing old agent resource", "namespace", modified.Namespace, "name", modified.Name, "gvk", gvk)
			err = c.localDynamicClient.Resource(mapping.Resource).Namespace(modified.Namespace).Delete(ctx, modified.Name, metav1.DeleteOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("deleting %s/%s for %s for agent cleanup: %w", modified.Namespace, modified.Name, gvk, err))
				continue
			}
		}
	}
	return merr.NewErrors(errs...)
}

func (c *Cleanup) CleanupReleases(ctx context.Context, key string, bd *fleet.BundleDeployment) error {
	c.cleanupOnce.Do(func() {
		go c.garbageCollect(ctx)
	})

	if bd != nil {
		return nil
	}
	return c.delete(ctx, key)
}

func (c *Cleanup) garbageCollect(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("garbage-collect")
	for {
		if err := c.cleanup(ctx, logger); err != nil {
			logger.Error(err, "failed to cleanup orphaned releases")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait.Jitter(c.garbageCollectionInterval, 1.0)):
		}
	}
}

func (c *Cleanup) cleanup(ctx context.Context, logger logr.Logger) error {
	deployed, err := c.helmDeployer.ListDeployments(c.helmDeployer.NewListAction())
	if err != nil {
		return err
	}

	for _, deployed := range deployed {
		bundleDeployment := &fleet.BundleDeployment{}
		err := c.client.Get(ctx, types.NamespacedName{Namespace: c.fleetNamespace, Name: deployed.BundleID}, bundleDeployment)
		if apierror.IsNotFound(err) {
			// found a helm secret, but no bundle deployment, so uninstall the release
			logger.Info("Deleting orphan bundle ID, helm uninstall", "bundleID", deployed.BundleID, "release", deployed.ReleaseName)
			if err := c.helmDeployer.DeleteRelease(ctx, deployed); err != nil {
				return err
			}

			return nil
		} else if err != nil {
			return err
		}

		key := releaseKey(c.defaultNamespace, bundleDeployment)
		if key != deployed.ReleaseName {
			// found helm secret and bundle deployment for BundleID, but release name doesn't match, so delete the release
			logger.Info("Deleting unknown bundle ID, helm uninstall", "bundleID", deployed.BundleID, "release", deployed.ReleaseName, "expectedRelease", key)
			if err := c.helmDeployer.DeleteRelease(ctx, deployed); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Cleanup) delete(ctx context.Context, bundleDeploymentKey string) error {
	_, name := kv.RSplit(bundleDeploymentKey, "/")
	return c.helmDeployer.Delete(ctx, name)
}

// releaseKey returns a deploymentKey from namespace+releaseName
func releaseKey(ns string, bd *fleet.BundleDeployment) string {
	if bd.Spec.Options.TargetNamespace != "" {
		ns = bd.Spec.Options.TargetNamespace
	} else if bd.Spec.Options.DefaultNamespace != "" {
		ns = bd.Spec.Options.DefaultNamespace
	}

	if bd.Spec.Options.Helm == nil || bd.Spec.Options.Helm.ReleaseName == "" {
		return ns + "/" + bd.Name
	}
	return ns + "/" + bd.Spec.Options.Helm.ReleaseName
}
