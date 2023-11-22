package manifest

import (
	"github.com/rancher/fleet/internal/content"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const SHA256SumAnnotation = "fleet.cattle.io/bundle-resources-sha256sum"

type Store interface {
	Store(manifest *Manifest) (string, error)
}

func NewStore(content fleetcontrollers.ContentController) Store {
	return &contentStore{
		contentCache: content.Cache(),
		content:      content,
	}
}

type contentStore struct {
	contentCache fleetcontrollers.ContentCache
	content      fleetcontrollers.ContentClient
}

func (c *contentStore) Store(manifest *Manifest) (string, error) {
	id, err := manifest.ID()
	if err != nil {
		return "", err
	}

	if _, err := c.contentCache.Get(id); err != nil && !apierrors.IsNotFound(err) {
		return "", err
	} else if err == nil {
		return id, nil
	}

	return id, c.createContents(id, manifest)
}

func (c *contentStore) createContents(id string, manifest *Manifest) error {
	data, err := manifest.Content()
	if err != nil {
		return err
	}
	digest, err := manifest.SHASum()
	if err != nil {
		return err
	}

	// Contents do not exist in the cluster
	compressed, err := content.Gzip(data)
	if err != nil {
		return err
	}

	_, err = c.content.Create(&fleet.Content{
		ObjectMeta: metav1.ObjectMeta{
			Name: id,
			Annotations: map[string]string{
				SHA256SumAnnotation: digest,
			},
		},
		Content: compressed,
	})
	return err
}
