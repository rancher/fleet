package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"

	"github.com/rancher/fleet/benchmarks/report"

	"github.com/onsi/gomega/gmeasure/table"
	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "report",
		Short: "report on a ginkgo json",
		Long:  `This is used to analyze benchmark results.`,
	}
	input   string
	db      string
	verbose bool
)

func main() {
	Execute()
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(jsonCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(csvCmd)
	rootCmd.PersistentFlags().StringVarP(&input, "input", "i", "report.json", "input file")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	reportCmd.PersistentFlags().StringVarP(&db, "db", "d", "db/", "path to json file database dir")
}

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

		calculate(sample, dsPop, scores)

		if verbose {
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
			fmt.Println(sample.Description)
			fmt.Println()
			fmt.Printf("Population size: %d\n", len(population.Samples))
			fmt.Println()
		}

		t1 := report.NewTable(sample.Setup)
		t1.TableStyle.EnableTextStyling = false
		fmt.Println(t1.Render())

		fmt.Println("# Individual Measurements")
		fmt.Println()

		mRows := map[string]map[string]MeasurementRow{}
		for experiment, xp := range sample.Experiments {
			mRows[experiment] = map[string]MeasurementRow{}
			for measurement, m := range xp.Measurements {
				row := MeasurementRow{
					Experiment:  experiment,
					Measurement: measurement,
					Value:       m.String(),
					Mean:        dsPop[experiment][measurement].Mean,
					StdDev:      dsPop[experiment][measurement].StdDev,
					ZScore:      dsPop[experiment][measurement].ZScore,
				}
				mRows[experiment][measurement] = row
			}
		}

		mTable := newMeasurementTable(mRows)
		fmt.Println(mTable.Render())

		// table for experiments
		fmt.Println("# Experiment Summary")
		fmt.Println()

		rows := map[string]Row{}
		for experiment, xp := range sample.Experiments {
			row := Row{
				Experiment: experiment,
				ZScore:     scores[experiment].MeanZScore,
			}
			row.Duration = xp.Measurements["TotalDuration"].Value
			row.MeanDuration = dsPop[experiment]["TotalDuration"].Mean
			row.StdDevDuration = dsPop[experiment]["TotalDuration"].StdDev
			rows[experiment] = row
		}

		xpTable := newTable(rows)
		fmt.Println(xpTable.Render())

		// Final score
		fmt.Println("# Total Score")
		fmt.Printf("%s, %.02f\n", input, scores.AvgZScores())

		return nil
	},
}

func prettyPrint(i interface{}) string {
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
}

func newTable(rows map[string]Row) *table.Table {
	t := table.NewTable()
	t.TableStyle.EnableTextStyling = false
	t.AppendRow(table.R(
		table.C("Experiment"),
		table.C("Duration"),
		table.C("Population Mean Duration"),
		table.C("Population StdDev Duration"),
		table.C("ZScore"),
		table.C(""),
		table.Divider("="),
		//"{{bold}}",
	))

	keys := slices.Sorted(maps.Keys(rows))
	for _, k := range keys {
		row := rows[k]

		r := table.R()
		t.AppendRow(r)
		r.AppendCell(table.C(row.Experiment))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.Duration)))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.MeanDuration)))
		r.AppendCell(table.C(fmt.Sprintf("%.02fs", row.StdDevDuration)))
		r.AppendCell(table.C(fmt.Sprintf("%.02f", row.ZScore)))
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

func newMeasurementTable(rows map[string]map[string]MeasurementRow) *table.Table {
	t := table.NewTable()
	t.TableStyle.EnableTextStyling = false
	t.AppendRow(table.R(
		table.C("Experiment"),
		table.C("Measurement"),
		table.C("Value"),
		table.C("Population Mean"),
		table.C("Population StdDev"),
		table.C("ZScore"),
		table.Divider("="),
		//"{{bold}}",
	))

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
			r.AppendCell(table.C(fmt.Sprintf("%.02f", row.StdDev)))
			if skip(row.Measurement) {
				r.AppendCell(table.C("-"))
			} else {
				r.AppendCell(table.C(fmt.Sprintf("%.02f", row.ZScore)))
			}
		}
	}

	return t
}
