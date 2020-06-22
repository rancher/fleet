package git

import (
	"errors"
	"fmt"
	"strings"
)

func firstField(lines []string, errText string) (string, error) {
	if len(lines) == 0 {
		return "", errors.New(errText)
	}

	fields := strings.Fields(lines[0])
	if len(fields) == 0 {
		return "", errors.New(errText)
	}

	if len(fields[0]) == 0 {
		return "", errors.New(errText)
	}

	return fields[0], nil
}

func formatRefForBranch(branch string) string {
	return fmt.Sprintf("refs/heads/%s", branch)
}
