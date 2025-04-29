package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var jsonCmd = &cobra.Command{
	Use:   "json",
	Short: "print report as JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		sample, err := loadSampleFile(input)
		if err != nil {
			return err
		}

		// clean up structs for export
		type row struct {
			Value string `json:"value,omitempty"`
			Units string `json:"units,omitempty"`
		}
		type export struct {
			Description string
			Experiments map[string]map[string]row
			Setup       map[string]row
		}

		s := export{
			Experiments: map[string]map[string]row{},
			Setup:       map[string]row{},
			Description: sample.Description,
		}
		for name, e := range sample.Experiments {
			if s.Experiments[name] == nil {
				s.Experiments[name] = map[string]row{}
			}
			for n, r := range e.Measurements {
				s.Experiments[name][n] = row{
					Value: r.String(),
					Units: r.Units,
				}
			}
		}
		for name, r := range sample.Setup {
			s.Setup[name] = row{
				Value: r.String(),
				Units: r.Units,
			}
		}

		fmt.Println(prettyPrint(s))

		return nil
	},
}
