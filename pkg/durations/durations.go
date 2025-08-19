package durations

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AgentRegistrationRetry         = time.Minute * 1
	AgentSecretTimeout             = time.Minute * 1
	ClusterImportTokenTTL          = time.Hour * 12
	ClusterRegisterDelay           = time.Second * 15
	ClusterRegistrationDeleteDelay = time.Minute * 40
	ClusterSecretRetry             = time.Second * 2
	ContentPurgeInterval           = time.Minute * 5
	CreateClusterSecretTimeout     = time.Minute * 30
	DefaultClusterCheckInterval    = time.Minute * 15
	DefaultImageInterval           = time.Minute * 15
	DefaultRequeueAfter            = time.Second * 5
	DefaultResyncAgent             = time.Minute * 30
	FailureRateLimiterBase         = time.Millisecond * 5
	FailureRateLimiterMax          = time.Second * 60
	SlowFailureRateLimiterBase     = time.Second * 2
	SlowFailureRateLimiterMax      = time.Minute * 10 // hit after 10 failures in a row
	GarbageCollect                 = time.Minute * 15
	RestConfigTimeout              = time.Second * 15
	ServiceTokenSleep              = time.Second * 2
	TokenClusterEnqueueDelay       = time.Second * 2
	// TriggerSleep is the delay before the driftdetect mini controller
	// starts watching deployed resources for changes
	TriggerSleep = time.Second * 5
	// GitRepoStatusDelay gives the gitjob controller some time to update
	// the gitrepo status first, before the status controller looks at
	// bundledeployments.
	GitRepoStatusDelay = time.Second * 5
	// HelmAppStatusDelay gives the helmapp controller some time to update
	// the helmapp status first, before the status controller looks at
	// bundledeployments.
	HelmAppStatusDelay = time.Second * 5
)

// Equal reports whether the duration t is equal to u.
func Equal(t *metav1.Duration, u *metav1.Duration) bool {
	if t == nil && u == nil {
		return true
	}
	if t != nil && u != nil {
		return t.Duration == u.Duration
	}
	return false
}
