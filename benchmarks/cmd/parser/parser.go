package parser

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2/types"
	gm "github.com/onsi/gomega/gmeasure"
	"gonum.org/v1/gonum/stat"
)

const (
	BeforeSetup = "beforeSetup"
	AfterSetup  = "afterSetup"
)

// Sample represents a single benchmark report
type Sample struct {
	Description string                 `json:"description,omitempty"`
	Experiments map[string]Experiment  `json:"experiments,omitempty"`
	Setup       map[string]Measurement `json:"setup,omitempty"`
}

// Experiment is a set of measurements, like from 50-gitrepo-1-bundle
// Measurements from the report are one dimensional, as most experiments don't
// use sampling
// type Experiment map[string]float64
type Experiment struct {
	Measurements map[string]Measurement
}

type Measurement struct {
	Value           float64            `json:"value,omitempty"`
	Type            gm.MeasurementType `json:"type,omitempty"`
	PrecisionBundle gm.PrecisionBundle `json:"precision_bundle,omitempty"`
	Style           string             `json:"style,omitempty"`
	Units           string             `json:"units,omitempty"`
}

func (r Measurement) String() string {
	return fmt.Sprintf(r.PrecisionBundle.ValueFormat, r.Value)
}

func NewSetup(specReports types.SpecReports, result map[string]Measurement) (string, error) {
	description := ""

	for _, specReport := range specReports {
		if len(specReport.ContainerHierarchyLabels) > 1 {
			continue
		}

		for _, entry := range specReport.ReportEntries {
			if entry.Name != "setup" {
				continue
			}

			xp := gm.Experiment{}
			data := entry.Value.AsJSON
			err := json.Unmarshal([]byte(data), &xp)
			if err != nil {
				fmt.Printf("error: %s\n", data)
				return "", err
			}
			// in report:
			// raw := entry.GetRawValue()
			// xp, ok := raw.(*gm.Experiment)
			// if !ok {
			// 	return nil, false
			// }
			//

			if xp.Name != BeforeSetup && xp.Name != AfterSetup {
				continue
			}

			for _, m := range xp.Measurements {
				name, v := Extract(m)
				if name != "" {
					tmp, ok := result[name]
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
					result[name] = tmp
				} else if m.Type == gm.MeasurementTypeNote {
					description += "\n"
					lines := strings.Split(strings.Trim(m.Note, "\n"), "\n")
					for i := range lines {
						description += fmt.Sprintf("%s\n", lines[i])
					}
				}
			}
			description += "\n"
		}
		break
	}

	return description, nil
}

func NewExperiments(specReports types.SpecReports, result map[string]Experiment) (float64, error) {
	var total float64
	for _, specReport := range specReports {
		if specReport.Failed() {
			return total, nil
		}

		// handle values from actual experiments, all experiments have labels
		if len(specReport.ContainerHierarchyLabels) <= 1 {
			continue
		}

		// NOTE we need to normalize the measurements, eg. high is
		// better or low is better, calculate difference from
		// Before/After.
		for _, entry := range specReport.ReportEntries {
			e := Experiment{
				Measurements: map[string]Measurement{},
			}

			xp := gm.Experiment{}
			data := entry.Value.AsJSON
			err := json.Unmarshal([]byte(data), &xp)
			if err != nil {
				fmt.Printf("error: %s\n", data)
				return total, err
			}
			// raw := entry.GetRawValue()
			// xp, ok := raw.(*gm.Experiment)
			// if !ok {
			// 	fmt.Printf("failed to access report: %#v\n", entry)
			// 	continue
			// }

			for _, m := range xp.Measurements {
				name, v := Extract(m)
				if name == "" {
					continue
				}

				if name == "TotalDuration" {
					total += v
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
			result[entry.Name] = e
		}
	}

	return total, nil
}

func Extract(m gm.Measurement) (string, float64) {
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
		v = m.Durations[0].Round(m.PrecisionBundle.Duration).Seconds()

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
