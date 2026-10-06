package utils

import (
	"context"
	"sync"

	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConflictInjector wraps a client and runs Inject once, the first time the
// target object is read. That makes the caller's in-memory copy stale by the
// time it patches the status, deterministically reproducing the race between a
// status reconciler and the controller that owns the same object.
//
// Match narrows which reads trigger the injection, so that reads of other kinds
// of object are ignored; it is required.
type ConflictInjector struct {
	client.Client

	Target types.NamespacedName
	Match  func(client.Object) bool
	Inject func()

	once sync.Once
}

func (c *ConflictInjector) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}

	if key == c.Target && c.Match(obj) {
		c.once.Do(c.Inject)
	}

	return nil
}

// StaleReadClient wraps a client and serves a pre-captured, out-of-date copy of
// the target object for the first N reads, simulating a lagging informer cache.
// Later reads pass through to the real client, as a cache that has caught up
// would.
//
// CopyInto writes the armed stale copy into the object being read, and reports
// whether that object was one this client should serve staleness for; it is
// required.
type StaleReadClient struct {
	client.Client

	Target   types.NamespacedName
	CopyInto func(into client.Object) bool

	mu            sync.Mutex
	staleUntilGet int
	reads         int
}

// Arm serves the stale copy for the next staleUntilGet reads of the target.
func (c *StaleReadClient) Arm(staleUntilGet int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staleUntilGet = staleUntilGet
	c.reads = 0
}

func (c *StaleReadClient) serveStale(key client.ObjectKey, obj client.Object) bool {
	if key != c.Target {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.staleUntilGet == 0 || c.reads >= c.staleUntilGet {
		return false
	}

	// CopyInto rejects objects of a kind this client does not serve, so only
	// count reads it actually handled.
	if !c.CopyInto(obj) {
		return false
	}
	c.reads++

	return true
}

func (c *StaleReadClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if c.serveStale(key, obj) {
		return nil
	}

	return c.Client.Get(ctx, key, obj, opts...)
}

// FindCondition returns the condition of the given type, and whether it exists.
func FindCondition(
	conds []genericcondition.GenericCondition,
	condType string,
) (genericcondition.GenericCondition, bool) {
	for _, c := range conds {
		if c.Type == condType {
			return c, true
		}
	}

	return genericcondition.GenericCondition{}, false
}
