// Package content purges orphaned content objects by inspecting bundledeployments in all namespaces. Runs every 5 minutes. (fleetcontroller)
package content

import (
	"context"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kv"
	"github.com/rancher/wrangler/pkg/ticker"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type handler struct {
	content          fleetcontrollers.ContentController
	bundleDeployment fleetcontrollers.BundleDeploymentController
	namespaces       corecontrollers.NamespaceClient
}

type contentRef struct {
	safeToDelete    bool
	markForDeletion bool
	bundleCount     int
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

	for range ticker.Context(ctx, durations.ContentPurgeInterval) {
		logrus.Debugf("Checking for orphaned content objects")
		namespaces, err := h.namespaces.List(metav1.ListOptions{})
		if err != nil {
			logrus.Warnf("Error reading namespaces %v", err)
			continue
		}
		var bundleDeployments []fleet.BundleDeployment
		for _, ns := range namespaces.Items {
			nsBundleDeployments, err := h.bundleDeployment.List(ns.Name, metav1.ListOptions{})
			if err != nil {
				logrus.Warnf("Error listing bundle deployments %v", err)
				continue
			}
			bundleDeployments = append(bundleDeployments, nsBundleDeployments.Items...)
		}

		contentRefs := make(map[string]*contentRef)

		contents, err := h.content.List(metav1.ListOptions{})
		if err != nil {
			logrus.Warnf("Error reading contents %v", err)
			continue
		}

		for _, content := range contents.Items {
			contentRefs[content.Name] = &contentRef{
				safeToDelete: false,
				bundleCount:  0,
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

		for contentName, cr := range contentRefs {
			_, deleteCandidate := deleteRefs[contentName]
			if cr.bundleCount > 0 {
				if deleteCandidate {
					delete(deleteRefs, contentName)
				}
			} else {
				if deleteCandidate {
					deleteRefs[contentName].safeToDelete = true
				} else {
					logrus.Infof("Marking orphaned content[%s] for deletion", contentName)
					deleteRefs[contentName] = &contentRef{
						bundleCount:     0,
						markForDeletion: true,
						safeToDelete:    false,
					}
				}
			}
		}

		for contentName, dr := range deleteRefs {
			if dr.safeToDelete {
				logrus.Infof("Deleting orphaned content[%s]", contentName)
				if err := h.content.Delete(contentName, &metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
					logrus.Warnf("Error deleting contentbundle %v", err)
				} else {
					delete(deleteRefs, contentName)
				}
			}
		}
	}
}
