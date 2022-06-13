// Package kubectl is a wrapper around the kubectl CLI
package kubectl

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"sync"
)

// use a mutex to workaround context being a global config
var mu sync.Mutex

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
	return c.Run(append([]string{"apply"}, args...)...)
}

func (c Command) Get(args ...string) (string, error) {
	return c.Run(append([]string{"get"}, args...)...)
}

func (c Command) Delete(args ...string) (string, error) {
	return c.Run(append([]string{"delete"}, args...)...)
}

func (c Command) Run(args ...string) (string, error) {
	if c.cnt != "" {
		mu.Lock()
		defer mu.Unlock()
		if out, err := c.exec("kubectl", "config", "use-context", c.cnt); err != nil {
			return out, err
		}
	}

	if c.ns != "" {
		args = append([]string{"-n", c.ns}, args...)
	}

	return c.exec("kubectl", args...)
}

func (c Command) exec(command string, args ...string) (string, error) {
	cmd := exec.Command(command, args...)

	var b bytes.Buffer
	if c.stdout {
		cmd.Stdout = io.MultiWriter(os.Stdout, &b)
		cmd.Stderr = io.MultiWriter(os.Stderr, &b)
	} else {
		cmd.Stdout = &b
		cmd.Stderr = &b
	}

	if c.dir != "" {
		cmd.Dir = c.dir
	}

	err := cmd.Run()
	return b.String(), err
}
