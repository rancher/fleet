package main

import (
	"github.com/rancher/fleet/modules/cli/cmds"
	"github.com/rancher/fleet/modules/cli/pkg/command"
)

func main() {
	command.Main(cmds.App())
}
