package apply

import (
	"testing"
)

func TestGetEffectiveMaxConcurrency(t *testing.T) {
	tests := map[string]struct {
		input    int
		expected int
	}{
		"zero defaults to 4":     {0, 4},
		"negative defaults to 4": {-1, 4},
		"custom value 8":         {8, 8},
		"custom value 16":        {16, 16},
		"custom value 12":        {12, 12},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := getEffectiveMaxConcurrency(tt.input); got != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, got)
			}
		})
	}
}
