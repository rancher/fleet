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
	LeaseDuration time.Duration

	// RenewDeadline is the duration that the acting controlplane will retry
	// refreshing leadership before giving up. Default is 10 seconds.
	RenewDeadline time.Duration

	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions. Default is 2 seconds.
	RetryPeriod time.Duration
}

// NewLeaderElectionOptions returns a new LeaderElectionOptions struct with the
// values parsed from environment variables.
func NewLeaderElectionOptions() (LeaderElectionOptions, error) {
	return NewLeaderElectionOptionsWithPrefix("CATTLE")
}

// NewLeaderElectionOptionsWithPrefix returns a new LeaderElectionOptions struct with the values
// parsed from environment variables with the given prefix.
//
// The environment variables passed to the fleet agentmanagement container in the fleet controller
// pod need to be prefixed differently to differentiate between those which are meant to configure
// the fleet controller and those which are meant to configure the fleet agentmanagement container,
// since the fleet agentmanagement controller creates the fleet deployment (for manager initiated
// deployments).
func NewLeaderElectionOptionsWithPrefix(prefix string) (LeaderElectionOptions, error) {
	var (
		leaderOpts LeaderElectionOptions
		err        error
		name       string
	)

	name = fmt.Sprintf("%s_ELECTION_LEASE_DURATION", prefix)
	leaderOpts.LeaseDuration, err = parseEnvDuration(name)
	if err != nil {
		return leaderOpts, err
	}

	name = fmt.Sprintf("%s_ELECTION_RENEW_DEADLINE", prefix)
	leaderOpts.RenewDeadline, err = parseEnvDuration(name)
	if err != nil {
		return leaderOpts, err
	}

	name = fmt.Sprintf("%s_ELECTION_RETRY_PERIOD", prefix)
	leaderOpts.RetryPeriod, err = parseEnvDuration(name)
	if err != nil {
		return leaderOpts, err
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

func parseEnvDuration(envVar string) (time.Duration, error) {
	if d := os.Getenv(envVar); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			return 0, fmt.Errorf("failed to parse %s with duration %s: %w", envVar, d, err)
		}
		return v, nil
	}
	return 0, fmt.Errorf("environment variable %s not set", envVar)
}
