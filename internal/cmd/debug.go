package cmd

// Copied from https://github.com/rancher/wrangler-cli

import (
	"flag"
	"fmt"

	"github.com/sirupsen/logrus"
	"k8s.io/klog/v2"
)

type DebugConfig struct {
	Debug      bool `usage:"Turn on debug logging"`
	DebugLevel int  `usage:"If debugging is enabled, set klog -v=X"`
}

func (c *DebugConfig) SetupDebug() error {
	logging := flag.NewFlagSet("", flag.PanicOnError)
	klog.InitFlags(logging)
	if c.Debug {
		logrus.SetLevel(logrus.DebugLevel)
		if err := logging.Parse([]string{
			fmt.Sprintf("-v=%d", c.DebugLevel),
		}); err != nil {
			return err
		}
	} else {
		if err := logging.Parse([]string{
			"-v=0",
		}); err != nil {
			return err
		}
	}

	return nil
}
