package command

import (
	"flag"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

type DebugConfig struct {
	Debug      bool
	DebugLevel int
}

func (c *DebugConfig) MustSetupDebug() {
	err := c.SetupDebug()
	if err != nil {
		panic("failed to setup debug logging: " + err.Error())
	}
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

func AddDebug(cmd *cobra.Command, config *DebugConfig) *cobra.Command {
	cmd.Flags().BoolVar(&config.Debug, "debug", false, "Turn on debug logging")
	cmd.Flags().IntVar(&config.DebugLevel, "debug-level", 0, "If debugging is enabled, set klog -v=X")
	return cmd
}
