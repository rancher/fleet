package manifest

import (
	"github.com/rancher/fleet/internal/content"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Lookup interface {
	Get(id string) (*Manifest, error)
}

func NewLookup(content fleetcontrollers.ContentClient) Lookup {
	return &lookup{
		content: content,
	}
}

type lookup struct {
	content fleetcontrollers.ContentClient
}

func (l *lookup) Get(id string) (*Manifest, error) {
	c, err := l.content.Get(id, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	bytes, err := content.GUnzip(c.Content)
	if err != nil {
		return nil, err
	}

	return readManifest(bytes, id)
}
