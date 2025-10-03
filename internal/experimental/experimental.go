package experimental

import (
	"os"
	"strconv"
)

const (
	SchedulesExperimentalFlag = "EXPERIMENTAL_SCHEDULES"
)

// SchedulesEnabled returns true if the EXPERIMENTAL_SCHEDULES env variable is set to true
// returns false otherwise
func SchedulesEnabled() bool {
	value, err := strconv.ParseBool(os.Getenv(SchedulesExperimentalFlag))
	return err == nil && value
}
