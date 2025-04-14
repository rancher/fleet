package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
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

// ParseEnvAgentReplicaCount parses the environment variable FLEET_AGENT_REPLICA_COUNT. If the
// environment variable is not set or the value cannot be parsed, it will return 1.
func ParseEnvAgentReplicaCount() int32 {
	replicas, err := parseEnvInt32("FLEET_AGENT_REPLICA_COUNT")
	if err != nil {
		logrus.Warn("FLEET_AGENT_REPLICA_COUNT not set, defaulting to 1")
		return 1
	}
	return replicas
}

// parseEnvInt32 parses an environment variable. It returns an error if the environment variable is
// not set or if it cannot be parsed as an int32.
func parseEnvInt32(envVar string) (int32, error) {
	if d, ok := os.LookupEnv(envVar); ok {
		v, err := strconv.ParseInt(d, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("failed to parse %s with int32 %s: %w", envVar, d, err)
		}
		return int32(v), nil
	}
	return 0, fmt.Errorf("environment variable %s not set", envVar)
}
