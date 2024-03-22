package durations

import "time"

const (
	AgentRegistrationRetry         = time.Minute * 1
	AgentSecretTimeout             = time.Minute * 1
	DefaultClusterEnqueueDelay     = time.Second * 15
	ClusterImportTokenTTL          = time.Hour * 12
	ClusterRegisterDelay           = time.Second * 15
	ClusterRegistrationDeleteDelay = time.Minute * 40
	ClusterSecretRetry             = time.Second * 2
	ContentPurgeInterval           = time.Minute * 5
	CreateClusterSecretTimeout     = time.Minute * 30
	DefaultClusterCheckInterval    = time.Minute * 15
	DefaultImageInterval           = time.Minute * 15
	DefaultResyncAgent             = time.Minute * 30
	FailureRateLimiterBase         = time.Millisecond * 5
	FailureRateLimiterMax          = time.Second * 60
	SlowFailureRateLimiterBase     = time.Second * 2
	SlowFailureRateLimiterMax      = time.Minute * 10 // hit after 10 failures in a row
	GarbageCollect                 = time.Minute * 15
	MonitorBundleDelay             = time.Minute * 5
	RestConfigTimeout              = time.Second * 15
	ServiceTokenSleep              = time.Second * 2
	TokenClusterEnqueueDelay       = time.Second * 2
	TriggerSleep                   = time.Second * 2
	DefaultCpuPprofPeriod          = time.Minute
	ReleaseCacheTTL                = time.Minute * 5
)
