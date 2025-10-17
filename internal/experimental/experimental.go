package experimental

import (
	"os"
	"strconv"
)

const (
	CopyResourcesDownstreamFlag = "EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM"
	SchedulesFlag               = "EXPERIMENTAL_SCHEDULES"
)

// CopyResourcesDownstreamEnabled returns true if the EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM env variable
// is set to true; returns false otherwise.
func CopyResourcesDownstreamEnabled() bool {
	value, err := strconv.ParseBool(os.Getenv(CopyResourcesDownstreamFlag))
	return err == nil && value
}

// SchedulesEnabled returns true if the EXPERIMENTAL_SCHEDULES env variable is set to true
// returns false otherwise
func SchedulesEnabled() bool {
	value, err := strconv.ParseBool(os.Getenv(SchedulesFlag))
	return err == nil && value
}
