package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/rancher/fleet/benchmarks"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

/*
#!/bin/bash
set -e

date=$(date +"%F_%T")
out="b-$date.json"
FLEET_BENCH_OUTPUT=${FLEET_BENCH_OUTPUT-$out}
FLEET_BENCH_TIMEOUT=${FLEET_BENCH_TIMEOUT-"5m"}
FLEET_BENCH_NAMESPACE=${FLEET_BENCH_NAMESPACE-"fleet-local"}
FLEET_BENCH_METRICS=${FLEET_BENCH_METRICS-"true"}

export FLEET_BENCH_TIMEOUT
export FLEET_BENCH_NAMESPACE
export FLEET_BENCH_METRICS

n=$(kubectl get clusters.fleet.cattle.io -n "$FLEET_BENCH_NAMESPACE" -l fleet.cattle.io/benchmark=true  -ojson | jq '.items | length')
if [ "$n" -eq 0 ]; then

	echo "No clusters found to benchmark"
	echo "You need at least one cluster with the label fleet.cattle.io/benchmark=true"
	echo
	echo "Example:"
	echo "kubectl label clusters.fleet.cattle.io -n fleet-local local fleet.cattle.io/benchmark=true"
	exit 1

fi

ginkgo run --fail-fast --seed 1731598958 --json-report "$FLEET_BENCH_OUTPUT" ./benchmarks
k = kubectl.New("", workspace)

go run ./benchmarks/cmd report -d benchmarks/db -i "$FLEET_BENCH_OUTPUT"
*/

// runCmd runs the ginkgo tests, it is configured via env variables.
//
// FLEET_BENCH_OUTPUT=${$out}
// FLEET_BENCH_TIMEOUT=${"5m"}
// FLEET_BENCH_NAMESPACE=${"fleet-local"}
// FLEET_BENCH_METRICS=${"true"}
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run benchmarks against current cluster",
	RunE: func(cmd *cobra.Command, args []string) error {

		// check clusters
		k := kubectl.New("", namespace)
		out, err := k.Get("clusters.fleet.cattle.io", "-l fleet.cattle.io/benchmark=true", `-ogo-template={{printf "%d" (len  .items)}}`)
		if err != nil {
			fmt.Println("Error getting clusters:", err)
			os.Exit(1)
		}
		n, err := strconv.Atoi(out)
		if err != nil {
			fmt.Println("Error reading number of clusters:", err)
			os.Exit(1)
		}
		if n < 1 {
			fmt.Println("No clusters found to benchmark")
			fmt.Println("You need at least one cluster with the label fleet.cattle.io/benchmark=true")
			fmt.Println()
			fmt.Println("Example:")
			fmt.Printf("kubectl label clusters.fleet.cattle.io -n %s local fleet.cattle.io/benchmark=true\n", namespace)
			os.Exit(1)
		}

		if _, err := os.Stat(db); errors.Is(err, os.ErrNotExist) {
			os.MkdirAll(db, 0755)
		}

		date := time.Now().Format("2006-01-02_15:04:05")
		report := fmt.Sprintf("b-%s.json", date)

		os.Setenv("FLEET_BENCH_OUTPUT", report)
		os.Setenv("FLEET_BENCH_TIMEOUT", timeout.String())
		os.Setenv("FLEET_BENCH_NAMESPACE", namespace)
		os.Setenv("FLEET_BENCH_METRICS", "true")
		if verbose {
			os.Setenv("FLEET_BENCH_VERBOSE", "true")
		}
		testing.MainStart(
			matchStringOnly(nil),
			[]testing.InternalTest{
				{"BenchmarkSuite", benchmarks.TestBenchmarkSuite},
			},
			nil, nil, nil,
		).Run()

		rootCmd.SetArgs([]string{
			"report",
			"--db=" + db,
			"--input=" + report,
		})
		if err := rootCmd.Execute(); err != nil {
			fmt.Println("Failed to run report command:", err)
			os.Exit(1)
		}

		fmt.Printf("Move the report %q to the %q folder, if you want to compare future benchmark against it.\n", report, db)
		return nil
	},
}

var errMain = errors.New("testing: unexpected use of func Main")

type corpusEntry = struct {
	Parent     string
	Path       string
	Data       []byte
	Values     []any
	Generation int
	IsSeed     bool
}

type matchStringOnly func(pat, str string) (bool, error)

func (f matchStringOnly) MatchString(pat, str string) (bool, error)   { return f(pat, str) }
func (f matchStringOnly) StartCPUProfile(w io.Writer) error           { return errMain }
func (f matchStringOnly) StopCPUProfile()                             {}
func (f matchStringOnly) WriteProfileTo(string, io.Writer, int) error { return errMain }
func (f matchStringOnly) ImportPath() string                          { return "" }
func (f matchStringOnly) StartTestLog(io.Writer)                      {}
func (f matchStringOnly) StopTestLog() error                          { return errMain }
func (f matchStringOnly) SetPanicOnExit0(bool)                        {}
func (f matchStringOnly) CoordinateFuzzing(time.Duration, int64, time.Duration, int64, int, []corpusEntry, []reflect.Type, string, string) error {
	return errMain
}
func (f matchStringOnly) RunFuzzWorker(func(corpusEntry) error) error { return errMain }
func (f matchStringOnly) ReadCorpus(string, []reflect.Type) ([]corpusEntry, error) {
	return nil, errMain
}
func (f matchStringOnly) CheckCorpus([]any, []reflect.Type) error { return nil }
func (f matchStringOnly) ResetCoverage()                          {}
func (f matchStringOnly) SnapshotCoverage()                       {}

func (f matchStringOnly) InitRuntimeCoverage() (mode string, tearDown func(string, string) (string, error), snapcov func() float64) {
	return
}
