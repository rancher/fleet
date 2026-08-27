package controller

import (
	"testing"
	"time"

	"github.com/rancher/fleet/pkg/durations"
)

// TestStaleCacheRequeueAfter checks that repeated stale cache observations back
// off, and that the wait stops growing at the cap: a cache which never catches
// up must not keep the agent polling the API server every few seconds.
func TestStaleCacheRequeueAfter(t *testing.T) {
	cases := []struct {
		hits     int
		expected time.Duration
	}{
		{hits: 1, expected: durations.StaleCacheRequeue},
		{hits: 2, expected: 2 * durations.StaleCacheRequeue},
		{hits: 3, expected: 4 * durations.StaleCacheRequeue},
		{hits: 100, expected: durations.StaleCacheRequeueMax},
	}

	for _, c := range cases {
		if got := staleCacheRequeueAfter(c.hits); got != c.expected {
			t.Errorf("staleCacheRequeueAfter(%d) = %s, expected %s", c.hits, got, c.expected)
		}
	}
}

// TestStaleCacheHits checks that the counter for a BundleDeployment starts at
// one, grows by one every time a stale cache hit is recorded, and is reset to 0
// when stale cache hits are forgotten.
func TestStaleCacheHits(t *testing.T) {
	r := &BundleDeploymentReconciler{}
	key := "cluster-ns/bd"

	for expected := 1; expected <= 3; expected++ {
		if got := r.recordStaleCacheHit(key); got != expected {
			t.Errorf("recordStaleCacheHit() = %d, expected %d", got, expected)
		}
	}

	// Another BundleDeployment is counted separately.
	if got := r.recordStaleCacheHit("cluster-ns/other-bd"); got != 1 {
		t.Errorf("recordStaleCacheHit() for a second key = %d, expected 1", got)
	}

	r.forgetStaleCacheHits(key)

	if got := r.recordStaleCacheHit(key); got != 1 {
		t.Errorf("recordStaleCacheHit() after forgetting = %d, expected 1", got)
	}
}
