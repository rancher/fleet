// Package kubectl is a wrapper around the kubectl CLI
package kubectl

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
)

type Command struct {
	cnt    string
	ns     string
	dir    string
	stdout bool
}

func New(cnt string, ns string) Command {
	return Command{cnt: cnt, ns: ns}
}

func (c Command) Context(cnt string) Command {
	n := c
	n.cnt = cnt
	return n
}

func (c Command) Namespace(ns string) Command {
	n := c
	n.ns = ns
	return n
}

func (c Command) Stdout(enable bool) Command {
	n := c
	n.stdout = enable
	return n
}

func (c Command) Workdir(dir string) Command {
	n := c
	n.dir = dir
	return n
}

func (c Command) Apply(args ...string) (string, error) {
	return c.Run(append([]string{"apply", "--wait"}, args...)...)
}

func (c Command) Get(args ...string) (string, error) {
	return c.Run(append([]string{"get"}, args...)...)
}

func (c Command) Delete(args ...string) (string, error) {
	return c.Run(append([]string{"delete"}, args...)...)
}

func (c Command) Create(args ...string) (string, error) {
	return c.Run(append([]string{"create"}, args...)...)
}

func (c Command) Logs(args ...string) (string, error) {
	return c.Run(append([]string{"logs"}, args...)...)
}

func (c Command) Patch(args ...string) (string, error) {
	return c.Run(append([]string{"patch"}, args...)...)
}

func (c Command) Label(args ...string) (string, error) {
	return c.Run(append([]string{"label"}, args...)...)
}

func (c Command) Run(args ...string) (string, error) {
	if c.cnt != "" {
		args = append([]string{"--context", c.cnt}, args...)
	}

	if c.ns != "" {
		args = append([]string{"-n", c.ns}, args...)
	}

	GinkgoWriter.Printf("kubectl %s\n", strings.Join(args, " "))
	stdout, stderr, err := c.exec("kubectl", args...)
	result := stdout + stderr
	if err != nil {
		GinkgoWriter.Printf("result:%s err:%s\n", result, err)
	}

	return result, err
}

// RunStdout behaves like Run but returns stdout and stderr separately.
func (c Command) RunStdout(args ...string) (stdout, stderr string, err error) {
	if c.cnt != "" {
		args = append([]string{"--context", c.cnt}, args...)
	}

	if c.ns != "" {
		args = append([]string{"-n", c.ns}, args...)
	}

	GinkgoWriter.Printf("kubectl %s\n", strings.Join(args, " "))
	stdout, stderr, err = c.exec("kubectl", args...)
	if err != nil {
		GinkgoWriter.Printf("stdout:%s stderr:%s err:%s\n", stdout, stderr, err)
	}

	return stdout, stderr, err
}

func (c Command) exec(command string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(context.Background(), command, args...)

	var outBuf, errBuf bytes.Buffer
	if c.stdout {
		cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
		cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	} else {
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
	}

	if c.dir != "" {
		cmd.Dir = c.dir
	}

	err := cmd.Run()
	return outBuf.String(), errBuf.String(), err
}
