package mocks

import (
	"time"

	quartz "github.com/reugn/go-quartz/quartz"
)

type MockScheduledJob struct {
	Detail          *quartz.JobDetail
	TriggerDuration time.Duration
}

func (m *MockScheduledJob) JobDetail() *quartz.JobDetail {
	return m.Detail
}

func (m *MockScheduledJob) Trigger() quartz.Trigger {
	return quartz.NewSimpleTrigger(m.TriggerDuration)
}
func (m *MockScheduledJob) NextRunTime() int64 {
	return 0
}
