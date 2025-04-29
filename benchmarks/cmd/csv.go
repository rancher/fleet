package main

import (
	"encoding/csv"
	"maps"
	"os"
	"regexp"
	"slices"

	"github.com/spf13/cobra"
)

var csvCmd = &cobra.Command{
	Use:   "csv",
	Short: "print report setup as CSV",
	RunE: func(cmd *cobra.Command, args []string) error {
		sample, err := loadSampleFile(input)
		if err != nil {
			return err
		}

		description := sample.Description
		re := regexp.MustCompile(`\{\{[^}]*\}\}`)
		description = re.ReplaceAllString(description, "")
		wre := regexp.MustCompile(`WARNING:.*`)
		description = wre.ReplaceAllString(description, "")

		records := [][]string{}

		headers := []string{"File", "Description"}
		values := []string{input, description}

		keys := slices.Sorted(maps.Keys(sample.Setup))
		for _, name := range keys {
			m := sample.Setup[name]

			headers = append(headers, name+" "+m.Units)
			values = append(values, m.String())
		}

		// append experiments duration
		keys = slices.Sorted(maps.Keys(sample.Experiments))
		for _, name := range keys {
			e := sample.Experiments[name]

			headers = append(headers, name)
			m := e.Measurements["TotalDuration"]
			values = append(values, m.String())
		}

		records = append(records, headers)
		records = append(records, values)

		w := csv.NewWriter(os.Stdout)
		w.Comma = ','

		for _, record := range records {
			if err := w.Write(record); err != nil {
				return err
			}
		}
		w.Flush()

		if err := w.Error(); err != nil {
			return err
		}

		return nil
	},
}
