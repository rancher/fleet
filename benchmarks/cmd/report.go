package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/rancher/fleet/benchmarks/report"

	"github.com/onsi/gomega/gmeasure/table"
	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "report on a ginkgo json",
	Long:  `This is used to compare benchmark results.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		population, err := loadDB(db)
		if err != nil {
			return err
		}
		// fmt.Print(prettyPrint(&population))

		sample, err := loadSampleFile(input)
		if err != nil {
			return err
		}

		dsPop := Dataset{}
		for _, s := range population.Samples {
			transformDataset(dsPop, s)
		}

		scores := scoresByXP{}

		if len(population.Samples) > 1 {
			calculate(sample, dsPop, scores)
		}

		if debug {
			fmt.Println("# Population from DB")
			fmt.Println("```")
			fmt.Println(prettyPrint(dsPop))
			fmt.Println("```")
			fmt.Println()
			fmt.Println("# Current Sample")
			fmt.Println("```")
			fmt.Println(prettyPrint(sample))
			fmt.Println("```")
			fmt.Println()
		}

		if sample.Description != "" {
			fmt.Println("# Description of Setup")
			fmt.Println()
			fmt.Println("> This section contains information about the setup used for the current sample. Like the k8s version, the node resources and the images available to the node.")
			fmt.Println(sample.Description)

			fmt.Println("Population Size")
			fmt.Println("===============")
			fmt.Printf("Reports in %q: %d\n", db, len(population.Samples))
			fmt.Println()
		}

		fmt.Println("# Results for Current Sample")
		fmt.Println()
		fmt.Println("> These measurements were taken before and after the experiments.")
		fmt.Println()

		t := report.NewTable(sample.Setup)
		t.TableStyle.EnableTextStyling = false
		fmt.Println(t.Render())

		if verbose {
			fmt.Println("# Compare Individual Measurements to Population")
			fmt.Println()
			fmt.Println("> For each experiment, the measurements for the current sample are compared to the population's data.")
			fmt.Println()

			rows := map[string]map[string]MeasurementRow{}
			for experiment, xp := range sample.Experiments {
				rows[experiment] = map[string]MeasurementRow{}
				for measurement, m := range xp.Measurements {
					row := MeasurementRow{
						Experiment:  experiment,
						Measurement: measurement,
						Value:       m.String(),
						Mean:        dsPop[experiment][measurement].Mean,
						StdDev:      dsPop[experiment][measurement].StdDev,
						ZScore:      dsPop[experiment][measurement].ZScore,
					}
					rows[experiment][measurement] = row
				}
			}

			t := newMeasurementTable(rows)
			fmt.Println(t.Render())
		}

		// table for experiments
		fmt.Println("# Summary for each Experiment")
		fmt.Println()
		fmt.Println("> The duration of each experiment is compared to the population's data.")
		if len(population.Samples) < 2 {
			fmt.Println("> There are not enough samples in the database folder. Cannot generate means and a standard score.")
		}
		fmt.Println()

		rows := map[string]Row{}
		for experiment, xp := range sample.Experiments {
			row := Row{
				Experiment: experiment,
				ZScore:     scores[experiment].MeanZScore,
			}
			row.Duration = xp.Measurements["TotalDuration"].Value
			if len(dsPop[experiment]["TotalDuration"].Values) > 0 {
				row.PopDuration = dsPop[experiment]["TotalDuration"].Values[0]
			}
			row.MeanDuration = dsPop[experiment]["TotalDuration"].Mean
			row.StdDevDuration = dsPop[experiment]["TotalDuration"].StdDev
			rows[experiment] = row
		}

		if len(population.Samples) > 1 {
			t = newTableZScore(rows)
		} else if len(population.Samples) == 1 {
			t = newTableCompare(rows)
		} else {
			t = newTable(rows)
		}
		fmt.Println(t.Render())

		fmt.Println("# Final Score")
		fmt.Println()
		fmt.Printf("%s: %.02f\n", input, scores.AvgZScores())

		return nil
	},
}

func prettyPrint(i any) string {
	s, _ := json.MarshalIndent(i, "", "\t")
	return string(s)
}

type MeasurementRow struct {
	Experiment  string
	Measurement string
	Value       string
	Mean        float64
	StdDev      float64
	ZScore      float64
}

type Row struct {
	Experiment     string
	Duration       float64
	MeanDuration   float64
	StdDevDuration float64
	ZScore         float64
	// used for direct compare, when population size is too small for zscore.
	PopDuration float64
}

func newTableZScore(rows map[string]Row) *table.Table {
	t := table.NewTable()
	t.TableStyle.EnableTextStyling = false
	row := table.R(
		table.C("Experiment"),
		table.C("Duration"),
		table.C("Mean Duration"),
		table.Divider("="),
	)
	if stats {
		row.AppendCell(table.C("StdDev Duration"))
		row.AppendCell(table.C("ZScore"))
	}
	row.AppendCell(table.C(""))
	t.AppendRow(row)

	keys := slices.Sorted(maps.Keys(rows))
	for _, k := range keys {
		row := rows[k]

		r := table.R()
		t.AppendRow(r)
		r.AppendCell(table.C(row.Experiment))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.Duration)))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.MeanDuration)))
		if stats {
			r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.StdDevDuration)))
			r.AppendCell(table.C(fmt.Sprintf("%.02f", row.ZScore)))
		}
		if row.ZScore < 0 {
			r.AppendCell(table.C("better"))
		} else if row.ZScore > 0 {
			r.AppendCell(table.C("worse"))
		} else {
			r.AppendCell(table.C(""))
		}

	}

	return t
}

func newTable(rows map[string]Row) *table.Table {
	t := table.NewTable()
	t.TableStyle.EnableTextStyling = false
	row := table.R(
		table.C("Experiment"),
		table.C("Duration"),
		table.Divider("="),
	)
	t.AppendRow(row)

	keys := slices.Sorted(maps.Keys(rows))
	for _, k := range keys {
		row := rows[k]

		r := table.R()
		t.AppendRow(r)
		r.AppendCell(table.C(row.Experiment))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.Duration)))
	}

	return t
}

func newTableCompare(rows map[string]Row) *table.Table {
	t := table.NewTable()
	t.TableStyle.EnableTextStyling = false
	row := table.R(
		table.C("Experiment"),
		table.C("Duration"),
		table.C("Compare to"),
		table.Divider("="),
		table.C(""),
	)
	t.AppendRow(row)

	keys := slices.Sorted(maps.Keys(rows))
	for _, k := range keys {
		row := rows[k]

		r := table.R()
		t.AppendRow(r)
		r.AppendCell(table.C(row.Experiment))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.Duration)))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.PopDuration)))
		if row.PopDuration > row.Duration {
			r.AppendCell(table.C("better"))
		} else {
			r.AppendCell(table.C("worse"))
		}

	}

	return t
}

func newMeasurementTable(rows map[string]map[string]MeasurementRow) *table.Table {
	t := table.NewTable()
	t.TableStyle.EnableTextStyling = false
	row := table.R(
		table.C("Experiment"),
		table.C("Measurement"),
		table.C("Value"),
		table.C("Mean"),
		table.Divider("="),
	)
	if stats {
		row.AppendCell(table.C("StdDev"))
		row.AppendCell(table.C("ZScore"))
	}
	t.AppendRow(row)

	keys := slices.Sorted(maps.Keys(rows))
	for _, k := range keys {
		rowGroup := rows[k]
		keys := slices.Sorted(maps.Keys(rowGroup))
		for _, k := range keys {
			row := rowGroup[k]

			r := table.R()
			t.AppendRow(r)
			r.AppendCell(table.C(row.Experiment))
			r.AppendCell(table.C(row.Measurement))
			r.AppendCell(table.C(row.Value))
			r.AppendCell(table.C(fmt.Sprintf("%.02f", row.Mean)))
			if stats {
				r.AppendCell(table.C(fmt.Sprintf("%.02f", row.StdDev)))
				if skip(row.Measurement) {
					r.AppendCell(table.C("-"))
				} else {
					r.AppendCell(table.C(fmt.Sprintf("%.02f", row.ZScore)))
				}
			}
		}
	}

	return t
}
