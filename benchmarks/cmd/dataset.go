package main

import (
	"math"
	"strings"

	"github.com/rancher/fleet/benchmarks/cmd/parser"

	"gonum.org/v1/gonum/stat"
)

type Dataset map[string]map[string]Measurements

// Measurements contains all measurements for an experiment in the population
// and some statistics.
type Measurements struct {
	Mean   float64
	StdDev float64
	ZScore float64
	Values []float64
}

type scoresByXP map[string]scores

type scores struct {
	ZScores    []float64
	Weights    []float64
	MeanZScore float64
}

func (s scoresByXP) AvgZScores() float64 {
	zscores := []float64{}
	for _, xp := range s {
		zscores = append(zscores, xp.ZScores...)
	}

	return stat.Mean(zscores, nil)
}

func skip(name string) bool {
	switch name {
	case "GCDuration", "Mem", "MemDuring", "ResourceCount":
		return true

	}
	return strings.HasPrefix(name, "RESTClient")
}

// transformDataset takes a sample and transforms it into a dataset.
// The output is organized by experiment, for example:
//
//	{ "50-gitrepo": {
//	   "CPU": { "mean": 0.5, "stddev": 0.1, values: [0.4, 0.5, 0.6] },
//	   "GC": { "mean": 0.5, "stddev": 0.1, values: [0.4, 0.5, 0.6] },
//	  },
//	 "50-bundle": {
//	   "CPU": { "mean": 0.5, "stddev": 0.1, values: [0.4, 0.5, 0.6] },
//	   "GC": { "mean": 0.5, "stddev": 0.1, values: [0.4, 0.5, 0.6] },
//	  },
//	}
func transformDataset(ds Dataset, sample parser.Sample) {
	for name, experiment := range sample.Experiments {
		for measurement, value := range experiment.Measurements {
			if _, ok := ds[name]; !ok {
				ds[name] = map[string]Measurements{}
			}
			if _, ok := ds[name][measurement]; !ok {
				ds[name][measurement] = Measurements{
					Values: []float64{},
				}
			}
			tmp := ds[name][measurement]
			tmp.Values = append(tmp.Values, value.Value)
			ds[name][measurement] = tmp
		}
	}
}

// calculate calculates the mean, stddev of the measurement. It calculates the zscore of the sample.
// This mutates dsPop and scores.
func calculate(sample *parser.Sample, dsPop Dataset, scores scoresByXP) {
	// foreach experiment in population, calculate mean and stddev
	for experiment, xp := range dsPop {
		for measurement, sg := range xp {
			mean, stddev := stat.MeanStdDev(sg.Values, nil)
			if math.IsNaN(stddev) || stddev == 0 {
				continue
			}

			if _, ok := sample.Experiments[experiment]; !ok {
				//fmt.Printf("missing experiment %s\n", name)
				continue
			}

			if _, ok := sample.Experiments[experiment].Measurements[measurement]; !ok {
				//fmt.Printf("missing measurement %s for experiments %s\n", measurement, name)
				continue
			}

			if skip(measurement) {
				continue
			}

			// calculate zscore
			m := sample.Experiments[experiment].Measurements[measurement]
			zscore := stat.StdScore(m.Value, mean, stddev)
			//fmt.Printf("zscore %s - %s %v %v %v\n", experiment, measurement, m, mean, zscore)

			// store in dsPop
			sg.Mean = mean
			sg.StdDev = stddev
			sg.ZScore = zscore
			dsPop[experiment][measurement] = sg

			// store to summarize by experiment
			xp := scores[experiment]
			xp.ZScores = append(xp.ZScores, zscore)
			xp.Weights = append(xp.Weights, weight(measurement))
			scores[experiment] = xp
		}
	}

	// Summarize experiments
	for name, xp := range scores {
		avg := stat.Mean(xp.ZScores, xp.Weights)
		xp.MeanZScore = avg
		scores[name] = xp
		//fmt.Printf("%s %v %v %v\n", name, avg, xp.ZScores, xp.Weights)
	}

}

// Some measurements have a higher volatility than others, or are duplicated.
// Only TotalDuration is used, as it is shown in :he result table.
//
// "CPU": 14.029999999999973,
// "GCDuration": 1.9185229570000004,
// "Mem": 4,
// "MemDuring": 4,
// "NetworkRX": 68288672,
// "NetworkTX": 30662826,
// "ReconcileErrors": 0,
// "ReconcileRequeue": 65,
// "ReconcileRequeueAfter": 462,
// "ReconcileSuccess": 2329,
// "ReconcileTime": 8153.420151420956,
// "ResourceCount": 300,
// "WorkqueueAdds": 2844,
// "WorkqueueQueueDuration": 3911.157310051014,
// "WorkqueueRetries": 527,
// "WorkqueueWorkDuration": 8169.425508522996
func weight(name string) float64 {
	if name == "TotalDuration" {
		return 1.0
	}

	return 0.0
}
