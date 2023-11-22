package manifest

import (
	"context"

	"github.com/rancher/fleet/internal/content"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewLookup() *Lookup {
	return &Lookup{}
}

type Lookup struct {
}

func (l *Lookup) Get(ctx context.Context, client client.Reader, id string) (*Manifest, error) {
	c := &fleet.Content{}
	err := client.Get(ctx, types.NamespacedName{Name: id}, c)
	if err != nil {
		return nil, err
	}

	data, err := content.GUnzip(c.Content)
	if err != nil {
		return nil, err
	}
	return FromJSON(data, c.SHA256Sum)
}
