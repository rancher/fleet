package troubleshooting_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rancher/fleet/pkg/troubleshooting"
)

func Test_OutputSummary(t *testing.T) {
	testCases := []struct {
		name           string
		writer         io.Writer
		snapshot       *troubleshooting.Snapshot
		expectedErrMsg string
	}{
		{
			name:           "nil snapshot",
			snapshot:       nil,
			expectedErrMsg: "nil snapshot",
		},
		{
			name:           "non-nil snapshot, nil writer",
			snapshot:       &troubleshooting.Snapshot{},
			expectedErrMsg: "nil writer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := troubleshooting.OutputSummary(tc.writer, tc.snapshot)

			if err == nil && tc.expectedErrMsg == "" {
				return
			}

			if !strings.Contains(err.Error(), tc.expectedErrMsg) {
				t.Fatalf("expected error matching %q, got: %v", tc.expectedErrMsg, err)
			}
		})
	}
}

func Test_OutputAll(t *testing.T) {
	testCases := []struct {
		name           string
		snapshots      []*troubleshooting.Snapshot
		expectedErrMsg string
	}{
		{
			name:      "empty snapshot slice",
			snapshots: []*troubleshooting.Snapshot{},
		},
		{
			name:      "only one snapshot in slice",
			snapshots: []*troubleshooting.Snapshot{nil},
		},
		{
			name:      "nil snapshots",
			snapshots: []*troubleshooting.Snapshot{nil, nil},
		},
		{
			name:           "non-nil snapshots, nil writer",
			snapshots:      []*troubleshooting.Snapshot{{}, {}},
			expectedErrMsg: "nil writer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := troubleshooting.OutputAll(nil, tc.snapshots)

			if err == nil && tc.expectedErrMsg == "" {
				return
			}

			if !strings.Contains(err.Error(), tc.expectedErrMsg) {
				t.Fatalf("expected error matching %q, got: %v", tc.expectedErrMsg, err)
			}
		})
	}
}

func Test_OutputDiff(t *testing.T) {
	testCases := []struct {
		name           string
		writer         io.Writer
		snapshots      []*troubleshooting.Snapshot
		expectedErrMsg string
	}{
		{
			name:           "empty snapshot slice",
			writer:         os.Stdout,
			snapshots:      []*troubleshooting.Snapshot{},
			expectedErrMsg: "need at least 2 snapshots",
		},
		{
			name:           "only one snapshot in slice",
			writer:         os.Stdout,
			snapshots:      []*troubleshooting.Snapshot{nil},
			expectedErrMsg: "need at least 2 snapshots",
		},
		{
			name:           "nil snapshots",
			writer:         os.Stdout,
			snapshots:      []*troubleshooting.Snapshot{nil, nil},
			expectedErrMsg: "need at least 2 snapshots",
		},
		{
			name:           "non-nil snapshots, nil writer",
			snapshots:      []*troubleshooting.Snapshot{{}, {}},
			expectedErrMsg: "nil writer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := troubleshooting.OutputDiff(tc.writer, tc.snapshots)

			if err == nil && tc.expectedErrMsg == "" {
				return
			}

			if !strings.Contains(err.Error(), tc.expectedErrMsg) {
				t.Fatalf("expected error matching %q, got: %v", tc.expectedErrMsg, err)
			}
		})
	}
}

func Test_OutputIssues(t *testing.T) {
	testCases := []struct {
		name           string
		writer         io.Writer
		snapshots      []*troubleshooting.Snapshot
		expectedErrMsg string
	}{
		{
			name:           "empty snapshot slice",
			writer:         os.Stdout,
			snapshots:      []*troubleshooting.Snapshot{},
			expectedErrMsg: "at least one snapshot is needed to output issues",
		},
		{
			name:           "nil snapshot",
			writer:         os.Stdout,
			snapshots:      []*troubleshooting.Snapshot{nil},
			expectedErrMsg: "nil snapshot",
		},
		{
			name:           "non-nil snapshot, nil writer",
			snapshots:      []*troubleshooting.Snapshot{{}},
			expectedErrMsg: "nil writer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := troubleshooting.OutputIssues(tc.writer, tc.snapshots)

			if err == nil && tc.expectedErrMsg == "" {
				return
			}

			if !strings.Contains(err.Error(), tc.expectedErrMsg) {
				t.Fatalf("expected error matching %q, got: %v", tc.expectedErrMsg, err)
			}
		})
	}
}

func Test_OutputDetailed(t *testing.T) {
	testCases := []struct {
		name           string
		writer         io.Writer
		snapshots      []*troubleshooting.Snapshot
		expectedErrMsg string
	}{
		{
			name:           "empty snapshot slice",
			writer:         os.Stdout,
			snapshots:      []*troubleshooting.Snapshot{},
			expectedErrMsg: "at least one snapshot is needed",
		},
		{
			name:           "nil snapshot",
			writer:         os.Stdout,
			snapshots:      []*troubleshooting.Snapshot{nil},
			expectedErrMsg: "nil snapshot",
		},
		{
			name:           "non-nil snapshot, nil writer",
			snapshots:      []*troubleshooting.Snapshot{{}},
			expectedErrMsg: "nil writer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := troubleshooting.OutputDetailed(tc.writer, tc.snapshots)

			if err == nil && tc.expectedErrMsg == "" {
				return
			}

			if !strings.Contains(err.Error(), tc.expectedErrMsg) {
				t.Fatalf("expected error matching %q, got: %v", tc.expectedErrMsg, err)
			}
		})
	}
}
