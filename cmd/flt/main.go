package main

import (
	"github.com/rancher/fleet/modules/cli/cmds"
	"github.com/rancher/fleet/modules/cli/pkg/command"

	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/apiextensions.k8s.io"
)

func main() {
	command.Main(cmds.App())
}
