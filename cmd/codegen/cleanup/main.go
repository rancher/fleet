package main

import (
	"os"

	"github.com/sirupsen/logrus"

	"github.com/rancher/wrangler/v3/pkg/cleanup"
)

func main() {
	if err := cleanup.Cleanup("./pkg/apis"); err != nil {
		logrus.Fatal(err)
	}
	if err := os.RemoveAll("./pkg/generated"); err != nil {
		logrus.Fatal(err)
	}
}
