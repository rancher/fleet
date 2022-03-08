package v1alpha1

import (
	"time"

	"github.com/robfig/cron/v3"
)

const DefaultDuration = "1h"

type Schedule struct {
	// Schedule window.
	Duration string `json:"duration,omitempty"`

	// Schedule Cron expression. Must include day of week.
	Cron string `json:"cron,omitempty"`
}

func (s *Schedule) GetCron() (cron.Schedule, error) {
	c, err := cron.ParseStandard(s.Cron)
	return c, err
}

func (s *Schedule) GetDuration() (time.Duration, error) {
	duration := DefaultDuration
	if s.Duration != "" {
		duration = s.Duration
	}
	return time.ParseDuration(duration)
}
