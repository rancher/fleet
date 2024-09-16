package desiredset

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/merr"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"

	"k8s.io/apimachinery/pkg/runtime"
)

// Indexer name added for cached types
const byHash = "wrangler.byObjectSetHash"

type desiredSet struct {
	client           *Client
	defaultNamespace string

	ratelimitingQps float32

	remove bool
	setID  string
	objs   *objectset.ObjectSet
	errs   []error

	plan Plan
}

func newDesiredSet(client *Client) desiredSet {
	return desiredSet{
		client:           client,
		defaultNamespace: defaultNamespace,
		ratelimitingQps:  1,
	}
}

func (o *desiredSet) err(err error) error {
	o.errs = append(o.errs, err)
	return o.Err()
}

func (o desiredSet) Err() error {
	return merr.NewErrors(append(o.errs, o.objs.Err())...)
}

func (o desiredSet) dryRun(ctx context.Context, ns string, setID string, objs ...runtime.Object) (Plan, error) {
	if ns == "" {
		o.defaultNamespace = defaultNamespace
	} else {
		o.defaultNamespace = ns
	}

	o.setID = setID

	o.objs = objectset.NewObjectSet()
	o.objs.Add(objs...)

	o.plan.Create = objectset.ObjectKeyByGVK{}
	o.plan.Update = PatchByGVK{}
	o.plan.Delete = objectset.ObjectKeyByGVK{}
	err := o.apply(ctx)
	return o.plan, err
}
