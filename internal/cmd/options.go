package cmd

import (
	"fmt"
	"os"
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

func parseEnvDuration(envVar string) (*time.Duration, error) {
	if d := os.Getenv(envVar); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s with duration %s: %w", envVar, d, err)
		}
		return &v, nil
	}
	return nil, nil
}
