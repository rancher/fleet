package managermanifest

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/rancher/fleet/pkg/config"

	"github.com/rancher/wrangler/pkg/yaml"
)

type Options struct {
	Namespace    string
	ManagerImage string
	AgentImage   string
	CRDsOnly     bool
}

func ManagerManifest(output io.Writer, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	assignDefaults(opts)

	cfg, err := marshalConfig(opts)
	if err != nil {
		return err
	}

	objs, err := objects(opts.Namespace, opts.ManagerImage, cfg, opts.CRDsOnly)
	if err != nil {
		return err
	}

	data, err := yaml.Export(objs...)
	if err != nil {
		return err
	}

	_, err = output.Write(data)
	return err
}

func marshalConfig(opts *Options) (string, error) {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(opts); err != nil {
		return "", err
	}
	return string(buf.Bytes()), nil
}

func assignDefaults(opts *Options) {
	if opts.AgentImage == "" {
		opts.AgentImage = config.DefaultAgentImage
	}
	if opts.ManagerImage == "" {
		opts.ManagerImage = config.DefaultManagerImage
	}
	if opts.Namespace == "" {
		opts.Namespace = config.DefaultNamespace
	}
}
