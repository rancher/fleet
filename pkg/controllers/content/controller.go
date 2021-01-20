package content

import (
	"context"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kv"
	"github.com/rancher/wrangler/pkg/ticker"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type handler struct {
	content          fleetcontrollers.ContentController
	bundleDeployment fleetcontrollers.BundleDeploymentController
	namespaces       corecontrollers.NamespaceClient
}

type contentRef struct {
	content      fleet.Content
	safeToDelete bool
	bundleCount  int
}

func Register(ctx context.Context,
	content fleetcontrollers.ContentController,
	bundleDeployment fleetcontrollers.BundleDeploymentController,
	namespaces corecontrollers.NamespaceController) {

	h := &handler{
		content:          content,
		bundleDeployment: bundleDeployment,
		namespaces:       namespaces,
	}

	go func() {
		h.purgeOrphaned(ctx)
	}()

}

func (h *handler) purgeOrphaned(ctx context.Context) {

	deleteRefs := make(map[string]*contentRef)

	for range ticker.Context(ctx, time.Minute*5) {
		logrus.Debugf("Checking for orphaned content objects")
		namespaces, err := h.namespaces.List(metav1.ListOptions{})
		if err != nil {
			logrus.Warnf("Error reading namespaces %v", err)
			continue
		}
		var bundleDeployments []fleet.BundleDeployment
		for _, ns := range namespaces.Items {
			nsBundleDeployments, err := h.bundleDeployment.List(ns.Name, metav1.ListOptions{})
			if err == nil {
				bundleDeployments = append(bundleDeployments, nsBundleDeployments.Items...)
			}
		}

		contentRefs := make(map[string]*contentRef)

		contents, err := h.content.List(metav1.ListOptions{})
		if err != nil {
			logrus.Warnf("Error reading contents %v", err)
			continue
		}

		for _, content := range contents.Items {
			contentRefs[content.Name] = &contentRef{
				content:     content,
				bundleCount: 0,
			}
		}

		for _, bd := range bundleDeployments {
			deployManifestID, _ := kv.Split(bd.Spec.DeploymentID, ":")
			if val, ok := contentRefs[deployManifestID]; ok {
				val.bundleCount++
			}

			stagedManifestID, _ := kv.Split(bd.Spec.StagedDeploymentID, ":")
			if val, ok := contentRefs[stagedManifestID]; ok && stagedManifestID != deployManifestID {
				val.bundleCount++
			}
		}

		for _, cr := range contentRefs {
			deleteRef, deleteCandidate := deleteRefs[cr.content.Name]
			if cr.bundleCount == 0 && deleteCandidate && deleteRef.safeToDelete {
				logrus.Infof("Deleting orphaned content[%s]", cr.content.Name)
				_ = h.content.Delete(cr.content.Name, &metav1.DeleteOptions{})
			} else if cr.bundleCount > 0 {
				if deleteCandidate {
					delete(deleteRefs, cr.content.Name)
				}
			} else {
				logrus.Infof("Marking orphaned content[%s] for deletion", cr.content.Name)
				deleteRefs[cr.content.Name] = &contentRef{
					content:      cr.content,
					bundleCount:  0,
					safeToDelete: true,
				}
			}
		}
	}
}
