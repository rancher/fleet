package report

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/onsi/ginkgo/v2"
	gm "github.com/onsi/gomega/gmeasure"
	"github.com/onsi/gomega/gmeasure/table"

	"gonum.org/v1/gonum/stat"
)

type Summary struct {
	Description string
	Experiments map[string]Experiment
	Setup       map[string]Measurement
}

type Measurement struct {
	Value           float64
	Type            gm.MeasurementType
	PrecisionBundle gm.PrecisionBundle
	Style           string
	Units           string
}

// Experiment is a set of measurements, like from 50-gitrepo-1-bundle
// Measurements from the report are one dimensional, as most experiments don't
// use sampling
type Experiment struct {
	Measurements map[string]Measurement
}

// ColorableString for ReportEntry to use
func (s Summary) ColorableString() string {
	sb := "{{green}}Experiments{{/}}\n"
	keys := slices.Sorted(maps.Keys(s.Experiments))
	for _, k := range keys {
		v := s.Experiments[k]
		sb += fmt.Sprintf("{{green}}%s{{/}}\n", k)
		t2 := newTable(v.Measurements)
		sb += t2.Render()
		sb += "\n"
	}
	sb += "{{green}}Environment{{/}}\n"
	sb += s.Description
	sb += "\n"
	t1 := newTable(s.Setup)
	sb += t1.Render()
	sb += "\n"
	return sb
}

// non-colorable String() is used by go's string formatting support but ignored by ReportEntry
func (s Summary) String() string {
	return fmt.Sprintf("%s\n%s\n%s\n", s.Description, prettyPrint(s.Setup), prettyPrint(s.Experiments))
}

func newTable(measurements map[string]Measurement) *table.Table {
	t := table.NewTable()
	t.AppendRow(table.R(
		table.C("Measurement"), table.C("Value"), table.C("Unit"),
		table.Divider("="),
		"{{bold}}",
	))

	keys := slices.Sorted(maps.Keys(measurements))
	for _, k := range keys {
		m := measurements[k]

		r := table.R(m.Style)
		t.AppendRow(r)
		r.AppendCell(table.C(k))
		r.AppendCell(table.C(fmt.Sprintf(m.PrecisionBundle.ValueFormat, m.Value)))
		r.AppendCell(table.C(m.Units))

	}

	return t
}

func New(r ginkgo.Report) (*Summary, bool) {
	s := &Summary{
		Experiments: map[string]Experiment{},
		Setup:       map[string]Measurement{},
	}

	for _, specReport := range r.SpecReports {
		if specReport.Failed() {
			return nil, false
		}

		// handle values from actual experiments, all experiments have labels
		if len(specReport.ContainerHierarchyLabels) <= 1 {
			continue
		}

		for _, entry := range specReport.ReportEntries {

			e := Experiment{
				Measurements: map[string]Measurement{},
			}

			raw := entry.GetRawValue()
			xp, ok := raw.(*gm.Experiment)
			if !ok {
				fmt.Printf("failed to access report: %#v\n", entry)
				continue
			}

			for _, m := range xp.Measurements {
				name, v := extract(m)
				if name == "" {
					continue
				}

				tmp, ok := e.Measurements[name]
				if ok {
					tmp.Value += v
				} else {
					tmp = Measurement{
						Value:           v,
						Type:            m.Type,
						PrecisionBundle: m.PrecisionBundle,
						Style:           m.Style,
						Units:           m.Units,
					}
				}
				e.Measurements[name] = tmp
			}
			s.Experiments[entry.Name] = e
		}
	}

	for _, specReport := range r.SpecReports {
		if len(specReport.ContainerHierarchyLabels) > 1 {
			continue
		}

		// handle setup entries
		for _, entry := range specReport.ReportEntries {
			if entry.Name != "setup" {
				continue
			}

			raw := entry.GetRawValue()
			xp, ok := raw.(*gm.Experiment)
			if !ok {
				return nil, false
			}

			if xp.Name != "beforeSetup" && xp.Name != "afterSetup" {
				continue
			}

			for _, m := range xp.Measurements {
				name, v := extract(m)
				if name != "" {
					tmp, ok := s.Setup[name]
					if ok {
						tmp.Value += v
					} else {
						tmp = Measurement{
							Value:           v,
							Type:            m.Type,
							PrecisionBundle: m.PrecisionBundle,
							Style:           m.Style,
							Units:           m.Units,
						}
					}
					s.Setup[name] = tmp
				} else if m.Type == gm.MeasurementTypeNote {
					s.Description += "\n"
					lines := strings.Split(strings.Trim(m.Note, "\n"), "\n")
					for i := range lines {
						s.Description += fmt.Sprintf("%s\n", lines[i])
					}
				}
			}
			s.Description += "\n"
		}
		break
	}

	return s, true
}

func extract(m gm.Measurement) (string, float64) {
	var v float64

	switch m.Type {
	case gm.MeasurementTypeValue:
		if len(m.Values) < 1 {
			return "", 0
		}
		v = m.Values[0]

	case gm.MeasurementTypeDuration:
		if len(m.Durations) < 1 {
			return "", 0
		}
		v = m.Durations[0].Seconds()

	default:
		return "", 0
	}

	name := m.Name

	// MemDuring is actually sampled, not a single value
	if m.Name == "MemDuring" {
		v = stat.Mean(m.Values, nil)
	} else if beforeAfterName(name) {
		if strings.HasSuffix(m.Name, "Before") {
			name = strings.TrimSuffix(m.Name, "Before")
			v = -v
		} else {
			name = strings.TrimSuffix(m.Name, "After")
		}
	}

	return name, v
}

// special handling for Before/After suffixes
func beforeAfterName(name string) bool {
	if strings.HasSuffix(name, "Before") {
		return true
	}
	if strings.HasSuffix(name, "After") {
		return true
	}
	return false
}

func prettyPrint(i interface{}) string {
	s, _ := json.MarshalIndent(i, "", "\t")
	return string(s)
}
