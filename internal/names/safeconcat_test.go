package names

import (
	"testing"
)

const (
	string32 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	string63 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	string64 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestSafeConcatName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  []string
		output string
	}{
		{
			name:   "empty input",
			output: "",
		},
		{
			name:   "single string",
			input:  []string{string63},
			output: string63,
		},
		{
			name:   "single long string",
			input:  []string{string64},
			output: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-ffe05",
		},
		{
			name:   "concatenate strings",
			input:  []string{"first", "second", "third"},
			output: "first-second-third",
		},
		{
			name:   "concatenate past 64 characters",
			input:  []string{string32, string32},
			output: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaaaaaaaaaa-da5ed",
		},
		{
			name:   "last character after truncation is not alphanumeric",
			input:  []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaa"},
			output: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-768c62",
		},
		{
			name:   "last characters after truncation aren't alphanumeric",
			input:  []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa--aaaaaaa"},
			output: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa--9e8cfe",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SafeConcatName(tt.input...); got != tt.output {
				t.Errorf("SafeConcatName() = %v, want %v", got, tt.output)
			}
		})
	}
}
