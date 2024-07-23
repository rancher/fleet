package manifest

import (
	"context"

	"github.com/rancher/fleet/internal/content"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewStore(client client.Client) *ContentStore {
	return &ContentStore{
		Client: client,
	}
}

type ContentStore struct {
	Client client.Client
}

// Store stores the manifest as a content resource.
// It copies the resources from the bundle to the content resource.
func (c *ContentStore) Store(ctx context.Context, manifest *Manifest) error {
	id, err := manifest.ID()
	if err != nil {
		return err
	}

	if err := c.Client.Get(ctx, types.NamespacedName{Name: id}, &fleet.Content{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	} else if err == nil {
		return nil
	}

	return c.createContents(ctx, id, manifest)
}

func (c *ContentStore) createContents(ctx context.Context, id string, manifest *Manifest) error {
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

	err = c.Client.Create(ctx, &fleet.Content{
		ObjectMeta: metav1.ObjectMeta{
			Name: id,
		},
		Content:   compressed,
		SHA256Sum: digest,
	})
	return err
}
