//go:build mage

package main

import (
	"os"
	"path"
)

func lookupLocalBin() string {
	pwd, err := os.Getwd()
	if err != nil {
		pwd = ""
	}
	p := path.Join(pwd, "bin")
	if v, found := os.LookupEnv("LOCALBIN"); found {
		p = v
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func findOrCreateLocalBin() (string, error) {
	p := lookupLocalBin()
	err := os.MkdirAll(p, 0755)
	if err != nil {
		return p, err
	}
	return p, nil
}
