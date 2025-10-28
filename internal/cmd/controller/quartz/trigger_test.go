package quartz

import (
	"testing"
	"time"
)

func TestControllerTrigger(t *testing.T) {
	interval := 1 * time.Second
	jitterPercent := 10
	tr := NewControllerTrigger(interval, jitterPercent)

	if tr.isInitRunDone {
		t.Errorf("unexpected initial value for isInitRunDone, expected false, got true")
	}

	// First fire time should be immediate
	now := time.Now().UnixNano()
	ft, err := tr.NextFireTime(now)
	if err != nil {
		t.Errorf("unexpected error on first call to NextFireTime: %v", err)
	}

	if !tr.isInitRunDone {
		t.Errorf("isInitRunDone should be true after first call, but it's false")
	}

	if ft != now {
		t.Errorf("unexpected first fire time, expected %d, got %d", now, ft)
	}

	// Second fire time should be after the interval + jitter
	nextFt, err := tr.NextFireTime(now)
	if err != nil {
		t.Errorf("unexpected error on second call to NextFireTime: %v", err)
	}

	// The next fire time should be within the interval + jitter range.
	minNextFt := now + interval.Nanoseconds()
	maxJitter := time.Duration(float64(interval) * float64(jitterPercent) / 100.0)
	maxNextFt := minNextFt + maxJitter.Nanoseconds()
	if nextFt < minNextFt || nextFt > maxNextFt {
		t.Errorf("unexpected next fire time, expected between %d and %d, got %d", minNextFt, maxNextFt, nextFt)
	}

	// Test description
	expectedDesc := "ControllerTrigger-1s"
	if tr.Description() != expectedDesc {
		t.Errorf("unexpected description, expected %q, got %q", expectedDesc, tr.Description())
	}
}

func TestJitter(t *testing.T) {
	baseDuration := 100 * time.Second
	testCases := []struct {
		name          string
		jitterPercent int
	}{
		{
			name:          "no jitter (0%)",
			jitterPercent: 0,
		},
		{
			name:          "negative jitter (-10%)",
			jitterPercent: -10,
		},
		{
			name:          "with jitter (10%)",
			jitterPercent: 10,
		},
		{
			name:          "with jitter (50%)",
			jitterPercent: 50,
		},
		{
			name:          "with jitter (100%)",
			jitterPercent: 100,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := jitter(baseDuration, tc.jitterPercent)

			if tc.jitterPercent <= 0 {
				if d != 0 {
					t.Errorf("expected duration 0 with no jitter, got %v", d)
				}
			} else {
				maxJitter := time.Duration(float64(baseDuration) * float64(tc.jitterPercent) / 100.0)

				if d < 0 || d > maxJitter {
					t.Errorf("duration %v is outside the expected range [0, %v]", d, maxJitter)
				}
			}
		})
	}
}
