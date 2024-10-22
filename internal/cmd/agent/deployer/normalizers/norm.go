package normalizers

import (
	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/diff"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Norm struct {
	normalizers []diff.Normalizer
}

func (n Norm) Normalize(un *unstructured.Unstructured) error {
	for _, normalizer := range n.normalizers {
		if err := normalizer.Normalize(un); err != nil {
			return err
		}
	}
	return nil
}

func New(lives objectset.ObjectByGVK, additions ...diff.Normalizer) Norm {
	n := Norm{
		normalizers: []diff.Normalizer{
			// Status fields are normally subresources which can't be influenced by resource updates
			&StatusNormalizer{},
			&MutatingWebhookNormalizer{
				Live: lives,
			},
			&ValidatingWebhookNormalizer{
				Live: lives,
			},
		},
	}

	n.normalizers = append(n.normalizers, additions...)

	return n
}
