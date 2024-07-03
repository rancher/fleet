package cmd

// Copied from https://github.com/rancher/wrangler-cli

import (
	"flag"
	"fmt"

	"github.com/sirupsen/logrus"
	"go.uber.org/zap/zapcore"

	"k8s.io/klog/v2"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
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

// OverrideZapOpts, for compatibility override zap opts with legacy debug opts.
func (c *DebugConfig) OverrideZapOpts(zopts *crzap.Options) *crzap.Options {
	if zopts == nil {
		zopts = &crzap.Options{}
	}

	zopts.Development = c.Debug

	if c.Debug {
		zopts.Level = zapcore.Level(c.DebugLevel * -1)
	}

	return zopts
}
