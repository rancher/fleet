package git

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"

	"github.com/pkg/errors"
)

func git(ctx context.Context, env []string, args ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), env...)

	var (
		out    bytes.Buffer
		errOut bytes.Buffer
	)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		return nil, errors.Wrap(err, errOut.String())
	}

	var output []string
	s := bufio.NewScanner(&out)
	for s.Scan() {
		output = append(output, s.Text())
	}

	return output, s.Err()
}
