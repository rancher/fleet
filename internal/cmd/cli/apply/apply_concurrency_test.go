package apply

import (
	"os"
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

func TestGetBundleCreationMaxConcurrency(t *testing.T) {
	tests := []struct {
		name          string
		envValue      string
		expectedValue int
		expectedError bool
	}{
		{
			name:          "default when env var not set",
			envValue:      "",
			expectedValue: 4,
			expectedError: false,
		},
		{
			name:          "custom value 8",
			envValue:      "8",
			expectedValue: 8,
			expectedError: false,
		},
		{
			name:          "custom value 16",
			envValue:      "16",
			expectedValue: 16,
			expectedError: false,
		},
		{
			name:          "invalid value returns error",
			envValue:      "not_a_number",
			expectedValue: 4,
			expectedError: true,
		},
		{
			name:          "zero is valid but caller handles default",
			envValue:      "0",
			expectedValue: 0,
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore the environment variable
			oldVal, wasSet := os.LookupEnv(BundleCreationMaxConcurrencyEnv)
			defer func() {
				if wasSet {
					os.Setenv(BundleCreationMaxConcurrencyEnv, oldVal)
				} else {
					os.Unsetenv(BundleCreationMaxConcurrencyEnv)
				}
			}()

			if tt.envValue != "" {
				os.Setenv(BundleCreationMaxConcurrencyEnv, tt.envValue)
			} else {
				os.Unsetenv(BundleCreationMaxConcurrencyEnv)
			}

			got, err := GetBundleCreationMaxConcurrency()
			if (err != nil) != tt.expectedError {
				t.Errorf("expected error %v, got %v", tt.expectedError, err != nil)
			}
			if got != tt.expectedValue {
				t.Errorf("expected %d, got %d", tt.expectedValue, got)
			}
		})
	}
}
