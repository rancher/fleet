package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type LeaderElectionOptions struct {
	// LeaseDuration is the duration that non-leader candidates will
	// wait to force acquire leadership. This is measured against time of
	// last observed ack. Default is 15 seconds.
	LeaseDuration *time.Duration

	// RenewDeadline is the duration that the acting controlplane will retry
	// refreshing leadership before giving up. Default is 10 seconds.
	RenewDeadline *time.Duration

	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions. Default is 2 seconds.
	RetryPeriod *time.Duration
}

// NewLeaderElectionOptions returns a new LeaderElectionOptions struct with the
// values parsed from environment variables.
func NewLeaderElectionOptions() (LeaderElectionOptions, error) {
	leaderOpts := LeaderElectionOptions{}
	if d := os.Getenv("CATTLE_ELECTION_LEASE_DURATION"); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			return leaderOpts, fmt.Errorf("failed to parse CATTLE_ELECTION_LEASE_DURATION with duration %s: %w", d, err)
		}
		leaderOpts.LeaseDuration = &v
	}
	if d := os.Getenv("CATTLE_ELECTION_RENEW_DEADLINE"); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			return leaderOpts, fmt.Errorf("failed to parse CATTLE_ELECTION_RENEW_DEADLINE with duration %s: %w", d, err)
		}
		leaderOpts.RenewDeadline = &v
	}
	if d := os.Getenv("CATTLE_ELECTION_RETRY_PERIOD"); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			return leaderOpts, fmt.Errorf("failed to parse CATTLE_ELECTION_RETRY_PERIOD with duration %s: %w", d, err)
		}
		leaderOpts.RetryPeriod = &v
	}
	return leaderOpts, nil
}

func ParseEnvAgentReplicaCount() (int32, error) {
	replicas, err := parseEnvInt32("FLEET_AGENT_REPLICA_COUNT")
	if err != nil {
		return 0, err
	}
	return replicas, nil
}

func parseEnvInt32(envVar string) (int32, error) {
	if d := os.Getenv(envVar); d != "" {
		v, err := strconv.ParseInt(d, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("failed to parse %s with int32 %s: %w", envVar, d, err)
		}
		return int32(v), nil
	}
	return 0, nil
}
