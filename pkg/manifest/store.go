package manifest

import (
	"sync"

	"github.com/rancher/fleet/pkg/content"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
	sync.RWMutex

	contentCache fleetcontrollers.ContentCache
	content      fleetcontrollers.ContentClient
}

func (c *contentStore) Store(manifest *Manifest) (string, error) {
	data, id, err := manifest.Content()
	if err != nil {
		return "", err
	}

	_, err = c.contentCache.Get(id)
	if err == nil {
		return id, nil
	} else if !apierrors.IsNotFound(err) {
		return "", err
	}

	compressed, err := content.Gzip(data)
	if err != nil {
		return id, err
	}

	_, err = c.content.Create(&fleet.Content{
		ObjectMeta: metav1.ObjectMeta{
			Name: id,
		},
		Content: compressed,
	})
	return id, err
}
