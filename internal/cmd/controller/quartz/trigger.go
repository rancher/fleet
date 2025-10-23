package quartz

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/reugn/go-quartz/quartz"
)

// ControllerTrigger is a custom trigger, implementing the quartz.Trigger interface. This trigger is
// used to schedule jobs to be run both:
// * periodically, after the first polling interval, as would happen with Quartz's `simpleTrigger`
// * right away, without waiting for that first polling interval to elapse.
// It also adds the possibility to add a percentage of jitter to the duration of the trigger to avoid
// situations in which we have many collisions.
type ControllerTrigger struct {
	IsInitRunDone bool
	jitterPercent int
	simpleTrigger *quartz.SimpleTrigger
}

func (t *ControllerTrigger) NextFireTime(prev int64) (int64, error) {
	if !t.IsInitRunDone {
		t.IsInitRunDone = true

		return prev, nil
	}

	simpleTriggerNext, err := t.simpleTrigger.NextFireTime(prev)
	if err != nil {
		return 0, err
	}

	return simpleTriggerNext + jitter(t.simpleTrigger.Interval, t.jitterPercent).Nanoseconds(), nil
}

func (t *ControllerTrigger) Description() string {
	return fmt.Sprintf("ControllerTrigger-%s", t.simpleTrigger.Interval)
}

func NewControllerTrigger(interval time.Duration, jitterPercent int) *ControllerTrigger {
	return &ControllerTrigger{
		jitterPercent: jitterPercent,
		simpleTrigger: quartz.NewSimpleTrigger(interval),
	}
}

// jitter returns a random jitter between 0% and +jitterPercent% of the original duration.
// jitterPercent is an integer percentage (e.g., 10 for 10%).
func jitter(d time.Duration, jitterPercent int) time.Duration {
	if jitterPercent <= 0 {
		return 0
	}

	// Convert jitter percent to a fraction
	jitterFraction := float64(jitterPercent) / 100.0

	// Calculate maximum jitter in float64 (nanoseconds)
	maxJitter := float64(d) * jitterFraction

	// Generate a random float64 between 0 and maxJitter
	return time.Duration(rand.Float64() * maxJitter) // nolint:gosec // non-crypto usage
}
