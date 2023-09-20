package main

import (
	"os"

	"github.com/rancher/gitjob/cmd/gitcloner/cmd"
	"github.com/rancher/gitjob/cmd/gitcloner/gogit"
	"github.com/sirupsen/logrus"
)

func main() {
	logrus.Info("Starting to clone git repository")
	cmd := cmd.New(gogit.NewCloner())
	if err := cmd.Execute(); err != nil {
		logrus.Errorf("Error cloning repository: %v", err)
		os.Exit(1)
	}
}
