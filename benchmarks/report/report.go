package report

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/rancher/fleet/benchmarks/cmd/parser"

	"github.com/onsi/ginkgo/v2"
	gm "github.com/onsi/gomega/gmeasure"
	"github.com/onsi/gomega/gmeasure/table"
)

type Summary struct {
	parser.Sample
}

// ColorableString for ReportEntry to use
func (s Summary) ColorableString() string {
	sb := "{{green}}Experiments{{/}}\n"
	keys := slices.Sorted(maps.Keys(s.Experiments))
	for _, k := range keys {
		v := s.Experiments[k]
		sb += fmt.Sprintf("{{green}}%s{{/}}\n", k)
		t2 := NewTable(v.Measurements)
		sb += t2.Render()
		sb += "\n"
	}
	sb += "{{green}}Environment{{/}}\n"
	sb += s.Description
	sb += "\n"
	t1 := NewTable(s.Setup)
	sb += t1.Render()
	sb += "\n"
	return sb
}

// non-colorable String() is used by go's string formatting support but ignored by ReportEntry
func (s Summary) String() string {
	return fmt.Sprintf("%s\n%s\n%s\n", s.Description, prettyPrint(s.Setup), prettyPrint(s.Experiments))
}

func NewTable(rows map[string]parser.Measurement) *table.Table {
	t := table.NewTable()
	t.AppendRow(table.R(
		table.C("Measurement"), table.C("Value"), table.C("Unit"),
		table.Divider("="),
		"{{bold}}",
	))

	keys := slices.Sorted(maps.Keys(rows))
	for _, k := range keys {
		m := rows[k]

		r := table.R(m.Style)
		t.AppendRow(r)
		r.AppendCell(table.C(k))
		r.AppendCell(table.C(m.String()))
		r.AppendCell(table.C(m.Units))

	}

	return t
}

// New builds a printable table from the ginkgo.Report.
func New(r ginkgo.Report) (*Summary, bool) {
	s := &Summary{
		parser.Sample{
			Experiments: map[string]parser.Experiment{},
			Setup:       map[string]parser.Measurement{},
		},
	}

	d, err := parser.NewSetup(r.SpecReports, s.Setup)
	if err != nil {
		return nil, false
	}
	s.Description = d

	total, err := parser.NewExperiments(r.SpecReports, s.Experiments)
	if err != nil {
		return nil, false
	}

	s.Setup["TotalDuration"] = parser.Measurement{
		Type:            gm.MeasurementTypeDuration,
		Style:           "{{bold}}",
		PrecisionBundle: gm.DefaultPrecisionBundle,
		Value:           total,
	}

	return s, true
}

func prettyPrint(i interface{}) string {
	s, _ := json.MarshalIndent(i, "", "\t")
	return string(s)
}
