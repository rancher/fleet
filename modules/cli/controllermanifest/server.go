package controllermanifest

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
	Output       string
}

func OperatorManifest(output io.Writer, opts *Options) error {
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
	config := config.Config{
		AgentImage: opts.AgentImage,
	}
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(config); err != nil {
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
