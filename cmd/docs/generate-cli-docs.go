package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	fleetagent "github.com/rancher/fleet/internal/cmd/agent"
	fleetcli "github.com/rancher/fleet/internal/cmd/cli"
	fleetcontroller "github.com/rancher/fleet/internal/cmd/controller"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	docDir := filepath.Join("./", os.Args[1])

	// fleet cli for gitjob
	cmd := fleetcli.App()
	cmd.DisableAutoGenTag = true

	err := generateCmdDoc(cmd, filepath.Join(docDir, "fleet-cli"))
	if err != nil {
		panic(err)
	}

	// fleet agent controller
	cmd = fleetagent.App()
	cmd.DisableAutoGenTag = true

	err = generateCmdDoc(cmd, filepath.Join(docDir, "fleet-agent"))
	if err != nil {
		panic(err)
	}

	// fleet controller
	cmd = fleetcontroller.App()
	cmd.DisableAutoGenTag = true

	err = generateCmdDoc(cmd, filepath.Join(docDir, "fleet-controller"))
	if err != nil {
		panic(err)
	}

}

// generateCmdDoc will generate the documentation for the given command, in the given directory
func generateCmdDoc(cmd *cobra.Command, dir string) error {
	if cmd.Hidden {
		return nil
	}

	// create the directory if it doesn't exist
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return errors.Wrapf(err, "error creating directory [%s]", dir)
	}

	// create the documentation for the given command
	err = createMarkdownFile(cmd, dir)
	if err != nil {
		return errors.Wrapf(err, "error creating markdown file for command [%s]", cmd.Name())
	}

	// create the documentation for its subcommands
	for _, subcmd := range cmd.Commands() {
		// if the subcommand does not have other subcommands, just generate the doc and continue
		if !subcmd.HasSubCommands() {
			err = createMarkdownFile(subcmd, dir)
			if err != nil {
				return errors.Wrapf(err, "error creating markdown file for command [%s]", subcmd.Name())
			}
			continue
		}

		// if the subcommand has other subcommands, then recurse in its own directory
		subdir := filepath.Join(dir, subcmd.Name())
		err = generateCmdDoc(subcmd, subdir)
		if err != nil {
			return errors.Wrapf(err, "error generating doc for command [%s]", subcmd.Name())
		}
	}

	return nil
}

// createMarkdownFile will create the markdown file for the given command in the given directory
func createMarkdownFile(cmd *cobra.Command, dir string) error {
	// skip 'help' command
	if cmd.Name() == "help" {
		return nil
	}

	basename := strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".md"
	filename := filepath.Join(dir, basename)

	f, err := os.Create(filename)
	if err != nil {
		return errors.Wrap(err, "error creating file")
	}
	defer f.Close()

	err = writeFileHeader(f, cmd.CommandPath())
	if err != nil {
		return errors.Wrapf(err, "error writing file header for command [%s]", cmd.Name())
	}

	err = doc.GenMarkdownCustom(cmd, f, linkHandler(cmd, dir))
	return errors.Wrap(err, "error generating markdown custom")
}

// linkHandler will return a function that will handle the markdown link generation
func linkHandler(cmd *cobra.Command, _ string) func(link string) string {
	return func(link string) string {
		link = strings.TrimSuffix(link, ".md")
		cmdPathLink := strings.ReplaceAll(link, "_", " ")

		// check if the link was referring to the parent command
		// we also need to check if the current command has subcommands, because if it does not then the link needs to point to the same directory
		if cmd.HasParent() && cmd.Parent().CommandPath() == cmdPathLink && cmd.HasSubCommands() {
			return "../" + link
		}

		for _, subcmd := range cmd.Commands() {
			// if the subcommand has other subcommands then it will have its own directory
			if subcmd.CommandPath() == cmdPathLink && subcmd.HasSubCommands() {
				return fmt.Sprintf("./%s/%s", subcmd.Name(), link)
			}
		}

		return "./" + link
	}
}

func writeFileHeader(w io.Writer, sidebarLabel string) error {
	_, err := fmt.Fprintf(w, `---
title: ""
sidebar_label: "%s"
---
`, sidebarLabel)

	return errors.Wrap(err, "error writing file header")
}

func usage() {
	fmt.Fprintln(os.Stdout, "Usage: ", os.Args[0], " <directory>")
}
